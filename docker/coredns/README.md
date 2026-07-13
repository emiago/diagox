
This creates local DNS that can handle SRV records

# How to setup

1.  setup local systemd to use port `1053` for our core dns

**NOTE: avoid .local for some reason systemd considers this for multicase**

```bash
sudo mkdir -p /etc/systemd/resolved.conf.d
echo -e "[Resolve]\nDomains=docker.dev\nDNS=127.0.0.1:1053" | sudo tee /etc/systemd/resolved.conf.d/docker-proxy.conf

# Resarts local systemd
sudo systemctl restart systemd-resolve
```

2. run core dns

```bash 
docker run --rm -it --name coredns -p 1053:53/udp -v $(pwd)/Corefile:/Corefile coredns/coredns:latest -conf /Corefile
```


3. test
```bash 
dig SRV _sip._udp.docker.dev                            

; <<>> DiG 9.18.24 <<>> SRV _sip._udp.docker.dev
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 42893
;; flags: qr rd ra; QUERY: 1, ANSWER: 2, AUTHORITY: 0, ADDITIONAL: 1

;; OPT PSEUDOSECTION:
; EDNS: version: 0, flags:; udp: 65494
;; QUESTION SECTION:
;_sip._udp.docker.dev.		IN	SRV

;; ANSWER SECTION:
_sip._udp.docker.dev.	60	IN	SRV	10 50 5060 docker-node-1.dev.
_sip._udp.docker.dev.	60	IN	SRV	10 50 5060 docker-node-2.dev.

;; Query time: 1 msec
;; SERVER: 127.0.0.53#53(127.0.0.53) (UDP)
;; WHEN: Fri Feb 07 12:25:15 CET 2025
;; MSG SIZE  rcvd: 123
```
