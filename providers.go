// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

type Providers struct {
	CDRStore  CDRStorage
	Registry  RegistryStore
	SIPTracer SIPTracer
	Cache     DialogExternalCache
	RateIn    *DialogRateLimiterIncoming
	RateOut   *DialogRateLimiterOutgoing
	Cleanup   []func()
}

func NewDefaultProviders(envConf EnvConfig) Providers {
	var siptracer SIPTracer
	if envConf.SIPCDRTrace {
		siptracer = NewSipTracerMemoryStorage()
	}

	return Providers{
		CDRStore:  NewCDRMemoryStorage(),
		Registry:  NewRegistryMemory(),
		SIPTracer: siptracer,
		RateIn:    NewDialogRateLimiterIncoming(envConf.RateLimiterIn),
		RateOut:   NewDialogRateLimiterOutgoing(envConf.RateLimiterOut),
	}
}

func (p *PBX) ApplyProviders(providers Providers) {
	if providers.CDRStore != nil {
		p.cdrStore = providers.CDRStore
	}
	if providers.SIPTracer != nil {
		p.sipTracer = providers.SIPTracer
	}
	if providers.Registry != nil {
		p.registry = providers.Registry
	}
	if providers.Cache != nil {
		p.cache = providers.Cache
	}
	if providers.RateIn != nil {
		p.rateLimiterInc = providers.RateIn
	}
	if providers.RateOut != nil {
		p.rateLimiterOut = providers.RateOut
	}
}

func (p *PBX) DialogCache() DialogExternalCache {
	return p.cache
}

func (p *PBX) SIPTracer() SIPTracer {
	return p.sipTracer
}

func SetNodeID(id string) {
	nodeID = id
}

func NodeID() string {
	return nodeID
}
