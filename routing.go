// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"strings"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo/sip"
)

type Router struct {
	conf Config
}

type endpointIndex struct {
	built       bool
	users       map[string][]ConfigEndpoint
	exactIPs    map[netip.Addr][]indexedEndpoint
	cidrs       []compiledCIDREndpoint
	dynamicUser *ConfigEndpoint
	defaultEnd  *ConfigEndpoint
}

type indexedEndpoint struct {
	order    int
	endpoint ConfigEndpoint
}

type compiledCIDREndpoint struct {
	order    int
	prefix   netip.Prefix
	endpoint ConfigEndpoint
}

// Routes call and fils URI
func (r *Router) RouteDialog(dialog *diago.DialogServerSession, uri *sip.Uri, auth *diago.DigestAuth) error {
	// How to build efficient router
	fromUser := dialog.FromUser()
	return r.Route(fromUser, uri, auth)
}

func (r *Router) MatchEndpoint(req *sip.Request, end *ConfigEndpoint) bool {
	index, err := r.endpointIndex()
	if err != nil {
		slog.Error("Failed to build endpoint index", "error", err)
		return false
	}

	fromUser := req.From().Address.User
	reqTransport := requestTransport(req)
	if endpoints, exists := index.users[fromUser]; exists {
		for _, e := range endpoints {
			if endpointTransportMatches(e, reqTransport) {
				*end = e
				return true
			}
		}
	}

	host, _, err := sip.ParseAddr(req.Source())
	if err != nil {
		slog.Error("Failed to parse source addr", "error", err)
		return false
	}
	sourceIP, err := netip.ParseAddr(host)
	if err != nil {
		slog.Error("Failed to parse source ip", "error", err)
		return false
	}
	sourceIP = sourceIP.Unmap()

	if e, exists := index.matchIP(sourceIP, reqTransport); exists {
		*end = e
		return true
	}

	if index.dynamicUser != nil && fromUser != "" && endpointTransportMatches(*index.dynamicUser, reqTransport) {
		*end = *index.dynamicUser
		return true
	}

	if index.defaultEnd != nil && endpointTransportMatches(*index.defaultEnd, reqTransport) {
		*end = *index.defaultEnd
		return true
	}

	return false
}

func (r *Router) MatchIncomingRoute(req *sip.Request, routeID string, route *ConfigRoute) bool {
	toAddress := req.To().Address
	toUser := toAddress.User

	// Multi tenant match
	// toHost := toAddress.Host

	// Check dids for this host

	// Get this route
	routeCtx, exists := r.conf.Routes[routeID]
	if !exists {
		return false
	}

	// For single hosted
	*route = func() ConfigRoute {
		for _, did := range routeCtx {
			if did.ID == toUser {
				return did
			}

			if did.Match == "prefix" && strings.HasPrefix(toUser, did.ID) {
				return did
			}

			if did.Match == "any" {
				if did.ID == "" {
					// Matches all
					return did
				}

				// Match as prefix and suffix
				if strings.Contains(toUser, did.ID) {
					return did
				}
			}
		}
		return ConfigRoute{}
	}()

	return route.Match == "any" || route.ID != ""
}

func (r *Router) MatchOutgoingRouteEndpoint(req *sip.Request, route *ConfigRoute, end *ConfigEndpoint) bool {
	endpoint := route.EndpointName
	toUser := req.To().Address.User
	if route.UseRegistry {
		slog.Debug("use registry enabled, try resolving registered user", "to_user", toUser, "endpoint", endpoint)
		if endpoint != "" {
			e, exists := r.conf.Endpoints[endpoint]
			if !exists {
				return false
			}
			e.Name = toUser
			e.useRegistry = true
			*end = e
			return true
		}

		regEnd, exists := r.findUserEndpoint(toUser)
		if exists {
			regEnd.useRegistry = true
			*end = regEnd
			return true
		}
		return false
	}

	if endpoint == "" {
		return false
	}

	// Now find endpoint
	e, exists := r.conf.Endpoints[endpoint]
	if !exists {
		return false
	}

	*end = e
	return true
}

func (r *Router) Route(fromUser string, uri *sip.Uri, auth *diago.DigestAuth) error {
	// How to build efficient router
	// How to know is this incoming or outgoing call

	// Outgoing:
	// - If call FROM matches user (endpoint)

	e, exists := r.findUserEndpoint(fromUser)
	if exists {
		auth.Username = e.Auth.Username
		auth.Password = e.Auth.Password
		return sip.ParseUri(e.URI, uri)

	}

	// It is incoming

	// Incoming:
	// - If does not match any user
	// -

	return fmt.Errorf("not found")
}

func (r *Router) findUserEndpoint(dstUser string) (ConfigEndpoint, bool) {
	index, err := r.endpointIndex()
	if err != nil {
		slog.Error("Failed to build endpoint index", "error", err)
		return ConfigEndpoint{}, false
	}
	e, exists := index.users[dstUser]
	if !exists || len(e) == 0 {
		return ConfigEndpoint{}, false
	}
	return e[0], true
}

func (r *Router) endpointIndex() (endpointIndex, error) {
	if r.conf.endpointIndex.built {
		return r.conf.endpointIndex, nil
	}
	index, err := buildEndpointIndex(r.conf.Endpoints, r.conf.endpointOrder)
	if err != nil {
		return endpointIndex{}, err
	}
	r.conf.endpointIndex = index
	return index, nil
}

func (idx endpointIndex) matchIP(ip netip.Addr, transport string) (ConfigEndpoint, bool) {
	matches := []indexedEndpoint{}
	matches = append(matches, idx.exactIPs[ip]...)
	for _, cidr := range idx.cidrs {
		if cidr.prefix.Contains(ip) {
			matches = append(matches, indexedEndpoint{
				order:    cidr.order,
				endpoint: cidr.endpoint,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].order < matches[j].order
	})
	for _, match := range matches {
		if endpointTransportMatches(match.endpoint, transport) {
			return match.endpoint, true
		}
	}
	return ConfigEndpoint{}, false
}

func buildEndpointIndex(endpoints map[string]ConfigEndpoint, order []string) (endpointIndex, error) {
	index := endpointIndex{
		built:    true,
		users:    map[string][]ConfigEndpoint{},
		exactIPs: map[netip.Addr][]indexedEndpoint{},
	}

	valueOrder := 0
	for _, name := range endpointNames(endpoints, order) {
		e := endpoints[name]
		switch e.Match.Type {
		case "user":
			index.users[e.Name] = append(index.users[e.Name], e)
		case "ip":
			for _, value := range e.Match.Values {
				ip, err := netip.ParseAddr(value)
				if err == nil {
					ip = ip.Unmap()
					index.exactIPs[ip] = append(index.exactIPs[ip], indexedEndpoint{order: valueOrder, endpoint: e})
					valueOrder++
					continue
				}

				prefix, err := netip.ParsePrefix(value)
				if err != nil {
					return endpointIndex{}, fmt.Errorf("endpoint %q: invalid ip match value %q", e.Name, value)
				}
				index.cidrs = append(index.cidrs, compiledCIDREndpoint{
					order:    valueOrder,
					prefix:   prefix.Masked(),
					endpoint: e,
				})
				valueOrder++
			}
		case "user_dynamic":
			if index.dynamicUser == nil {
				endpoint := e
				index.dynamicUser = &endpoint
			}
		case "":
			if index.defaultEnd == nil {
				endpoint := e
				index.defaultEnd = &endpoint
			}
		}
	}

	return index, nil
}

func endpointTransportMatches(endpoint ConfigEndpoint, transport string) bool {
	want := normalizeTransport(endpoint.Match.Transport)
	if want == "" {
		return true
	}
	return transport != "" && transport == want
}

func requestTransport(req *sip.Request) string {
	return normalizeTransport(req.Transport())
}

func normalizeTransport(transport string) string {
	return sip.NetworkToLower(strings.TrimSpace(transport))
}

func endpointNames(endpoints map[string]ConfigEndpoint, order []string) []string {
	names := make([]string, 0, len(endpoints))
	added := make(map[string]struct{}, len(endpoints))
	for _, name := range order {
		if _, exists := endpoints[name]; !exists {
			continue
		}
		names = append(names, name)
		added[name] = struct{}{}
	}

	extra := make([]string, 0, len(endpoints)-len(names))
	for name := range endpoints {
		if _, exists := added[name]; exists {
			continue
		}
		extra = append(extra, name)
	}
	sort.Strings(extra)
	names = append(names, extra...)
	return names
}
