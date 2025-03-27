# diagox

<img src="images/diagox-icon-blue.png" width="150" height="150" alt="GOPHONE">

*Dialog Go Exchange*

**diagox** is modern approach for simple Back To Back VOIP solution built on top of [sipgo](https://github.com/emiago/sipgo) and [diago](https://github.com/emiago/diago) library.
Ultimate goal is to allow you to scale your VOIP infrastructure, with monitoring of all calls.

**Main Features**:
- Call Bridging with inbound/outbound routing and media proxy.
- Call history with full SIP traffic and voice quality monitoring, with GUI and API
- Optional: running in multi node for scaling and HA.


> Project is free to use, and based on feedback it will be considered to take some path of development and maintance together with rest of Go VOIP libraries.


## Features

- Call bridging with no audio transcoding
- Integrated Registrar to allow user registering and discovery
- Call Rate Limiting / Inbound + Outbound
- Simple routing and fast matching
- Fallback routing based on SIP Response
- Endpoint IP/Auth identification
- Call History with SIP Trace, RTCP metrics and Quality calculations
- Call Recording to WAV format / Mixed streams
- Integrated monitoring with statsviz (only for debuging)
- Configuration validation
- Structured logging

## Install 

Application is single binary, you can just [download](https://github.com/emiago/diagox/releases/latest/download/diagox) from latest release and run it.

Running:
```bash
wget https://github.com/emiago/diagox/releases/latest/download/diagox
chmox +x diagox
./diagox
```

It needs `diagox.yaml` for your routing configuration. Example below.
```bash
nano diagox.yaml
```

## Configuration

Configuration is simple based on yaml. This offers higher automation and easier managing.

`diagox.yaml`
```yaml
version: "2.4"


# transports for SIP
transports: 
  udp:
    transport: "udp"
    bind: 0.0.0.0
    port: 5060
    external_host: my.domain.com # Use env SIP_EXTERNAL_HOST to set for all transports
    external_media_ip: 1.2.3.4 # Use env SIP_EXTERNAL_MEDIA_IP to set for all transports
  tcp:
    transport: "tcp"
    bind: 0.0.0.0
    port: 5060
  udp_local:
    transport: "udp"
    bind: 127.0.0.1
    port: 5099
  
# routes allow you to customize which endpoint your number should reach
# - `default` route context is special one which is always used unless incoming endpoint did not override
# - you can create more routes and split logic as you like
# - keeping route context small can improve call route matching
routes:
  # Default context goes every call from all endpoints
  # If not created or overided it has this configuration
  default:
    - id: ""
      match: "any" # Match any number
      use_registry: true # Check registry. Any register user will be reached

  incoming: 
    # Order here matters 
    # Required fields are: id and endpoint

    # Example with strict number matching
    - id: "4912345678" 
      endpoint: carrier_internal # On which endpoint to send

    # Example with prefix matching
    - id: "381"
      match: prefix
      endpoint: carrier_internal

    # Example with prefix matching and passing SIP headers
    - id: "121"
      match: prefix
      endpoint: carrier_internal
      sip_headers_pass: ["X-Myheader", "X-Account-ID"] # This headers are copied from incoming call to outgoing
      sip_headers: # This headers are added on outgoing channel.
        X-Fixed-Header: "Call121"

  # Example of outgoing where you want to have your internal endpoint route calls out
  outgoing: 
    - id: "987" 
      match: "prefix"
      endpoint: carrier_external

    # Example where to match all if did not match any of previous
    # Instead of endpoint we just want to hangup call. This is where hangup module is provided
    - id: ""
      match: "any" #Match all
      hangup:
        code: 404
  
  # Example when you want to route call but also do some fallback if initial endpoint/carrier failed
  with_fallback:
    - id: 49
      match: "prefix"
      endpoint: carrier_primary
      fallback: 
        codes: [401, 404, 487]
        endpoints: ["carrier_second", "carrier_third"]   

# Endpoints define way to identify your incoming SIP traffic and which route to use.
# Identifing is done with `match`. Supported values are: ip, user
# - `ip` is your incoming source IP
# - `user` matches by SIP From header and USER property  ex. From: <sip:$USER@example.com>
# Match order is: user, ip
endpoints:
  carrier_external:
    route: incoming
    match: 
      type: "ip" 
      values: ["182.168.0.0/24"]
    
    # Auth will identify incoming SIP INVITE/REGISTER additionally with Digest authentication
    auth:
      username: "test" 
      password: "test123"

    # Uri is for your outgoing or how to reach this carrier. 
    # If you use this endpoint in routes, this SIP uri will be used to reach. User part is replaced by caller ID.
    uri: "sip:carrier.external.com:5080"
    transport: tcp # You can force which transport to use. This is id of transport defined in transports
  
  carrier_internal:
    # When matched call will be sent to outgoing route
    route: outgoing

    # Incoming options
    match: 
      type: "ip"
      values: ["127.0.0.1/8"]

  alice: # -> alice@tenant1.domain.com
    # routing: default # If not defined `default`` route is used
    match: 
      type: "user" 
    auth:
      username: "alice" 
      password: "test123"    

  bob: # -> alice@tenant1.domain.com
    match: 
      type: "user" 
    auth:
      username: "carrier" 
      password: "test123" 
    
```

## Global configuration

More features are offered using global configuration or toogling some features.

```go
// Log formating.
string  "LOG_LEVEL" envDefault:"info" // debug, info, warn, error
string  "LOG_FORMAT" envDefault:"console" // json, console
// Rate limiting for incoming
bool    "RATE_LIMITER_IN_ENABLED" envDefault:"false"
int64   "RATE_LIMITER_IN_DIALOG_RPS"
int64   "RATE_LIMITER_IN_DIALOG_MAX"
// Rate limiting for outgoing
bool    "RATE_LIMITER_OUT_ENABLED" envDefault:"false"
int64   "RATE_LIMITER_OUT_DIALOG_RPS"
// Default outbound dial uri if not defined by endpoint
string   "OUTBOUND_DIAL_URI" envDefault:"sip:test@127.0.0.222:5066"
string   "CONF_FILE" envDefault:"diagox.yaml"
bool     "CDR_ENABLE" envDefault:"true"
string   "RECORDINGS_PATH" envDefault:"recordings"
bool     "FRONTEND_ENABLE" envDefault:"false"
string   "SIP_BIND_IP" envDefault:""     // Useful for pods/nodes that have dedicated IP
string   "SIP_EXTERNAL_IP" envDefault:"" // Useful for pods/nodes that have dedicated IP
// SIP_HOSTNAME identifies that message are matching this hostname. Used in registrar for example
string SIPHostname   `env:"SIP_HOSTNAME" envDefault:""`
```

## Multi node running 

**NOTE**: This is not yet stable and needs more testing. 

Dependencies
- MySQL - CDR/config storing
- Redis - Registry/Dialog caching

Will be shared soon.