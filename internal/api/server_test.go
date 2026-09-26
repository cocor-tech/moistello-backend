package api_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api"
)

func testConfig() config.ServerConfig {
	return config.ServerConfig{
		Host:            "127.0.0.1",
		Port:            0,
		ReadTimeout:     5 * time.Second,
		WriteTimeout:    5 * time.Second,
		MaxHeaderBytes:  1 << 20,
		ShutdownTimeout: 15 * time.Second,
	}
}

func get(t *testing.T, client *http.Client, url string) (int, error) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// TestServer_RollingRestart_NoFailedRequests simulates a rolling restart: a
// readiness-aware balancer keeps sending traffic until PreDrain flips the
// replica to not-ready, the server keeps the listener open for the
// configured delay so the balancer can react, and every request accepted
// before that point must complete successfully even though the handler is
// still running when the listener closes.
func TestServer_RollingRestart_NoFailedRequests(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	var order []string
	var orderMu sync.Mutex
	record := func(stage string) {
		orderMu.Lock()
		defer orderMu.Unlock()
		order = append(order, stage)
	}
	var lastHandlerDone atomic.Int64
	var closeLastAt atomic.Int64

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond) // longer than the delay: still in flight when the listener closes
		lastHandlerDone.Store(time.Now().UnixNano())
		w.WriteHeader(http.StatusOK)
	})
	cfg := testConfig()
	cfg.ShutdownDelay = 100 * time.Millisecond
	srv := api.NewServer(handler, cfg, api.ShutdownHooks{
		PreDrain:  []func(){func() { ready.Store(false); record("pre-drain") }},
		Drain:     []func(context.Context){func(context.Context) { record("drain") }},
		CloseLast: []func(){func() { closeLastAt.Store(time.Now().UnixNano()); record("close-last") }},
	})
	require.NoError(t, srv.Start())
	url := "http://" + srv.Addr() + "/"
	client := &http.Client{Timeout: 5 * time.Second}

	var served, failed atomic.Int64
	var firstFailure atomic.Value
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ready.Load() {
				code, err := get(t, client, url)
				if err != nil || code != http.StatusOK {
					failed.Add(1)
					if err != nil {
						firstFailure.CompareAndSwap(nil, err.Error())
					}
					continue
				}
				served.Add(1)
			}
		}()
	}

	time.Sleep(400 * time.Millisecond) // let traffic build up
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	begin := time.Now()
	require.NoError(t, srv.Shutdown(ctx))
	assert.GreaterOrEqual(t, time.Since(begin), cfg.ShutdownDelay, "listener stays open for the configured delay")
	wg.Wait()

	assert.Zero(t, failed.Load(), "requests accepted before the replica left rotation must succeed: %v", firstFailure.Load())
	assert.Greater(t, served.Load(), int64(16), "traffic must have been flowing during shutdown")
	assert.Equal(t, []string{"pre-drain", "drain", "close-last"}, order)
	assert.Greater(t, closeLastAt.Load(), lastHandlerDone.Load(), "pools close only after the last request finished")

	_, err := get(t, client, url)
	require.Error(t, err, "listener must be closed after shutdown")
}

func TestServer_ForcedKillAfterConfiguredTimeout(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})
	var closeLastRan atomic.Bool
	cfg := testConfig()
	cfg.ShutdownTimeout = 100 * time.Millisecond
	srv := api.NewServer(handler, cfg, api.ShutdownHooks{
		CloseLast: []func(){func() { closeLastRan.Store(true) }},
	})
	require.NoError(t, srv.Start())
	defer close(release)

	reqErr := make(chan error, 1)
	go func() {
		_, err := get(t, &http.Client{}, "http://"+srv.Addr()+"/")
		reqErr <- err
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	begin := time.Now()
	err := srv.Shutdown(ctx)
	assert.ErrorIs(t, err, api.ErrDrainDeadlineExceeded)
	assert.Less(t, time.Since(begin), 2*time.Second, "forced kill must not wait for the stuck handler")
	assert.True(t, closeLastRan.Load(), "pools still close after a forced kill")

	select {
	case err := <-reqErr:
		require.Error(t, err, "the stuck request is cut off, not left hanging")
	case <-time.After(2 * time.Second):
		t.Fatal("stuck request was not terminated by the forced close")
	}
}

func TestServer_StartFailsWhenPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	cfg := testConfig()
	cfg.Port = ln.Addr().(*net.TCPAddr).Port
	srv := api.NewServer(http.NotFoundHandler(), cfg, api.ShutdownHooks{})
	require.Error(t, srv.Start())
}

func TestRunServerWithHooks_SIGTERMDrainsAndReturns(t *testing.T) {
	var stages []string
	var mu sync.Mutex
	record := func(s string) { mu.Lock(); stages = append(stages, s); mu.Unlock() }
	listening := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	done := make(chan error, 1)
	go func() {
		done <- api.RunServerWithHooks(handler, testConfig(), api.ShutdownHooks{
			PreDrain:  []func(){func() { record("pre-drain") }},
			Drain:     []func(context.Context){func(context.Context) { record("drain") }},
			CloseLast: []func(){func() { close(listening); record("close-last") }},
		})
	}()

	time.Sleep(100 * time.Millisecond) // let the signal handler install
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("RunServerWithHooks did not return after SIGTERM")
	}
	<-listening
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"pre-drain", "drain", "close-last"}, stages)
}

func TestRunServer_LegacyCallbacksStillRun(t *testing.T) {
	ran := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- api.RunServer(http.NotFoundHandler(), testConfig(), func(context.Context) { ran <- struct{}{} })
	}()
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("RunServer did not return after SIGTERM")
	}
	select {
	case <-ran:
	default:
		t.Fatal("legacy shutdown callback did not run")
	}
}

func TestServer_ShutdownErrorIsTyped(t *testing.T) {
	assert.True(t, errors.Is(api.ErrDrainDeadlineExceeded, api.ErrDrainDeadlineExceeded))
}
