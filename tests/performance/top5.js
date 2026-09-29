import http from 'k6/http';
import ws from 'k6/ws';
import { check } from 'k6';
import { Trend } from 'k6/metrics';

const baseURL = __ENV.BASE_URL || 'http://127.0.0.1:1100';
const wsURL = baseURL.replace(/^http/, 'ws') + '/ws';
const token = __ENV.ACCESS_TOKEN;
const duration = __ENV.K6_DURATION || '20s';
const vus = Number(__ENV.K6_VUS || 5);

const loginLatency = new Trend('login_latency', true);
const circleLatency = new Trend('circle_list_latency', true);
const contributionLatency = new Trend('contributions_latency', true);
const balanceLatency = new Trend('wallet_balance_latency', true);
const websocketLatency = new Trend('websocket_connect_latency', true);

export const options = {
  scenarios: {
    auth_login: { executor: 'constant-vus', exec: 'authLogin', vus, duration },
    circle_list: { executor: 'constant-vus', exec: 'circleList', vus, duration },
    contributions: { executor: 'constant-vus', exec: 'contributions', vus, duration },
    wallet_balance: { executor: 'constant-vus', exec: 'walletBalance', vus, duration },
    websocket_connect: { executor: 'constant-vus', exec: 'websocketConnect', vus: Math.max(1, Math.floor(vus / 2)), duration },
  },
  thresholds: {
    checks: ['rate>0.99'],
    login_latency: ['p(95)<500'],
    circle_list_latency: ['p(95)<300'],
    contributions_latency: ['p(95)<350'],
    wallet_balance_latency: ['p(95)<300'],
    websocket_connect_latency: ['p(95)<500'],
  },
};

function authHeaders() { return { Authorization: `Bearer ${token}` }; }

export function setup() {
  if (!token) throw new Error('ACCESS_TOKEN is required; run against the seeded performance stack');
  const ready = http.get(`${baseURL}/health/live`);
  check(ready, { 'local stack is ready': (r) => r.status === 200 });
}

export function authLogin() {
  const response = http.post(`${baseURL}/v1/auth/nonce`, JSON.stringify({
    walletAddress: 'GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC',
  }), { headers: { 'Content-Type': 'application/json' }, tags: { endpoint: 'auth_login' } });
  loginLatency.add(response.timings.duration);
  check(response, { 'auth login accepted': (r) => r.status === 200 });
}

export function circleList() {
  const response = http.get(`${baseURL}/v1/circles?limit=20`, {
    headers: authHeaders(), tags: { endpoint: 'circle_list' },
  });
  circleLatency.add(response.timings.duration);
  check(response, { 'circle list returned': (r) => r.status === 200 });
}

export function contributions() {
  const response = http.get(`${baseURL}/v1/contributions?limit=20`, {
    headers: authHeaders(), tags: { endpoint: 'contributions' },
  });
  contributionLatency.add(response.timings.duration);
  check(response, { 'contributions returned': (r) => r.status === 200 });
}

export function walletBalance() {
  const response = http.get(`${baseURL}/v1/wallets/balance`, {
    headers: authHeaders(), tags: { endpoint: 'wallet_balance' },
  });
  balanceLatency.add(response.timings.duration);
  check(response, { 'wallet balance returned': (r) => r.status === 200 });
}

export function websocketConnect() {
  const started = Date.now();
  const response = ws.connect(wsURL, { headers: authHeaders(), tags: { endpoint: 'websocket_connect' } }, (socket) => {
    socket.on('open', () => {
      websocketLatency.add(Date.now() - started);
      socket.close();
    });
    socket.setTimeout(() => socket.close(), 1000);
  });
  check(response, { 'websocket upgraded': (r) => r && r.status === 101 });
}
