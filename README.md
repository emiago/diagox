# gopbx
GOPBX is modern, scalable and simple telephony solution built on top of sipgo and diago library.

GOPBX goal will be to quickly bridge internal, external calls. Add you numbers (DID) or build extensions
with scalable registry to handle high amount devices.

Features:
- Provides call history with full SIP traffic and voice quality monitoring. 
- It can be scalled to handle high ammount of calls with horizontal scalling. 
- It can run in **multi node** with High Availablitliy.
 

Configuration is simple based on yaml. This offers higher automation and easier managing.
Example:
```yaml
transports: 
  udp:
    transport: "udp"
    bind: 0.0.0.0
    port: 5060
  tcp:
    transport: "tcp"
    bind: 0.0.0.0
    port: 5060

routes:
  default: # Default context goes every call from all endpoints
    - id: "4912345678" # E164 
      endpoint: carrier_internal
    - id: "123"
      match: prefix
      endpoint: carrier_internal
      recording: false

  outgoing: # Outgoing routes all calls
    # Order here matters 
    - id: "49" 
      match: "prefix"
      endpoint: carrier_external

endpoints:
   # WIP
```

**MORE WILL BE SHARED**
