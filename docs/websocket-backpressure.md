# WebSocket Backpressure & Slow Client Management

## Overview
To prevent unbounded memory growth and resource exhaustion caused by slow or unresponsive WebSocket consumers, the Moistello Backend enforces bounded per-connection send buffers and a proactive disconnection policy.

## Architecture & Configuration
- **Per-Connection Buffer**: Each active WebSocket client connection maintains a buffered channel (`send chan []byte`) with a fixed capacity of 256 messages.
- **Non-blocking Dispatch**: During room broadcasts and direct user messages, transmissions to client buffers are non-blocking (`select ... case client.Send <- data: default: ...`).
- **Slow Client Disconnection**: If a client's send buffer becomes completely saturated, the message is dropped, the event is recorded in metrics, and the client is safely disconnected and unregistered from the Hub.

## Exposed Prometheus Metrics
- `moistello_websocket_dropped_messages_total`: Total count of broadcast messages dropped due to slow client buffers.
- `moistello_websocket_slow_clients_disconnected_total`: Total count of slow clients forcefully disconnected.
- `moistello_websocket_active_connections`: Current active WebSocket connection count.
