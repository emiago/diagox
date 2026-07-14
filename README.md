# Diagox

<p align="center">
  <img src="images/diagox-icon.png" width="140" alt="Diagox">
</p>

<p align="center">
  <strong>A self-hosted SIP and WebRTC voice gateway</strong><br>
  Route, bridge, and connect real-time communications across SIP, RTP, and WebRTC.
</p>

Diagox is a programmable voice gateway for connecting PBXs, SIP providers,
WebRTC clients, internal voice services, and other reachable telephony
endpoints. It runs as a single Go binary and is built on top of
[sipgo](https://github.com/emiago/sipgo) and
[diago](https://github.com/emiago/diago).

## What Diagox provides

- SIP ingress and egress over UDP, TCP, and WebSocket transports
- SIP dialog routing and bridging between endpoints
- WebRTC media and browser-friendly SIP/WebSocket connectivity
- Registrar and in-memory contact registry support
- Programmable routes with prefixes, fallback destinations, and hangup rules
- RTP media proxying, call recording, and VoIP monitoring
- YAML-based configuration suitable for local deployments and containers

## Get started

The fastest way to explore Diagox is to start with the minimal configuration:

```bash
go install ./cmd/diagox

CONF_FILE=example-configs/diagox-minimal.yaml diagox
```

The configuration expects an endpoint named `bob` at
`sip:internal.network:5080`. Update that destination for your environment.

For container-based deployments, build the included image and provide the
configuration through the image or a mounted file:

```bash
docker build -t diagox .
docker run --rm \
  -p 5060:5060/udp \
  -v "$PWD/example-configs/diagox-minimal.yaml:/app/diagox.yaml:ro" \
  diagox
```

### Diago load test

The repository includes a Diago-based load generator. It runs an in-process
UAS on port `5080`, originates concurrent calls to the configured Diagox SIP
target, and sends/receives PCMU RTP for every answered call. Configure Diagox
to route its outbound leg to `sip:127.0.0.1:5080`, then run:

```bash
go run ./cmd/diagox-loadtest \
  -target sip:1000@127.0.0.1:5060 \
  -uas-listen 127.0.0.1:5080 \
  -calls 100 \
  -rate 10 \
  -concurrency 50 \
  -duration 60s
```

Use `-calls 0` to run until interrupted, `-media=false` for SIP-only load,
and `-metrics-addr :9091` to expose load-generator metrics at
`http://localhost:9091/metrics`. Diagox exposes its application metrics at
`http://localhost:6060/metrics`.

See the [installation guide](https://emiago.github.io/diagox/docs/install/)
for runtime configuration, networking, and deployment details.

## Example configurations

All example configurations are included in the repository:

| Example | Use it for |
| --- | --- |
| [`diagox-minimal.yaml`](example-configs/diagox-minimal.yaml) | A small SIP routing and recording setup. |
| [`diagox-webrtc.yaml`](example-configs/diagox-webrtc.yaml) | SIP plus WebRTC/WebSocket endpoints. |
| [`diagox_full.yaml`](example-configs/diagox_full.yaml) | A broader example with route contexts, carrier endpoints, authentication, and header handling. |
| [`diagox_docker.yaml`](example-configs/diagox_docker.yaml) | Docker-oriented transports, registry routing, and carrier fallbacks. |

The examples contain placeholder addresses and credentials. Replace them before
using any configuration outside a local test environment.

## Core concepts

Diagox configuration is organized around three pieces:

- **Transports** define where Diagox listens for SIP traffic, including UDP,
  TCP, and WebSocket listeners.
- **Endpoints** identify incoming callers or define destinations such as PBXs,
  carriers, registered users, and WebRTC clients.
- **Routes** decide where calls go based on the endpoint, dialed number,
  prefixes, registration state, and other matching rules.

This keeps the call-routing policy separate from the service process and makes
it possible to adapt the same gateway to different networks and providers.

## Documentation

- [Getting started](https://emiago.github.io/diagox/docs/)
- [Installation](https://emiago.github.io/diagox/docs/install/)
- [WebRTC setup](https://emiago.github.io/diagox/docs/webrtc/)
- [Feature overview](https://emiago.github.io/diagox/docs/#features)

## Open source and scalable (enterprise)

The open-source Diagox distribution provides a focused, single-instance voice
gateway that can be run as a binary or container.

Scalable capabilities are available separately for deployments
that need:

- Multiple coordinated Diagox instances
- Kubernetes deployment charts
- External registry caching
- Database-backed call detail records
- Centralized management of instances and ingress configuration

For more information about scalable deployments, contact
[mail](mailto:emirfreelance91@gmail.com).

## Development

Build the server with:

```bash
go install ./cmd/diagox
```

Run the Go test suite with:

```bash
go test ./...
```

Diagox is licensed under the [Mozilla Public License 2.0](LICENSE.txt).
