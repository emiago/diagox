// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"errors"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"
)

var (
	ErrRegistryDoesNotExist = errors.New("registry does not exist")
)

type RegistryStore interface {
	// TODO add context
	RegisterBindingSet(id string, r RegisterBinding) error
	RegisterBindingDelete(id string) error
	RegisterBindingContactDelete(id string, contactAddress sip.Uri) error
	RegisterBindingGet(id string) (RegisterBinding, error)
}

type RegisterBinding struct {
	Aor        sip.Uri
	CallID     string
	Contacts   []sip.Uri
	Expiry     time.Duration
	TenantID   string // TODO should be mapped from tenant_host to id
	SourceAddr string
	Transport  string
}

type RegistryMemory struct {
	m map[string]RegisterBinding
	sync.RWMutex
}

func NewRegistryMemory() *RegistryMemory {
	return &RegistryMemory{
		m: make(map[string]RegisterBinding),
	}
}

func (r *RegistryMemory) RegisterBindingSet(id string, rec RegisterBinding) error {
	r.Lock()
	r.m[id] = rec
	r.Unlock()
	return nil
}

func (r *RegistryMemory) RegisterBindingDelete(id string) error {
	r.Lock()
	delete(r.m, id)
	r.Unlock()
	return nil
}

func (r *RegistryMemory) RegisterBindingContactDelete(id string, contact sip.Uri) error {
	r.Lock()
	rec, ok := r.m[id]
	if !ok {
		return ErrRegistryDoesNotExist
	}
	for i, c := range rec.Contacts {
		if c.String() == contact.String() {
			rec.Contacts = append(rec.Contacts[:i], rec.Contacts[i+1:]...)
			break
		}
	}
	r.Unlock()
	return nil
}

func (r *RegistryMemory) RegisterBindingGet(id string) (RegisterBinding, error) {
	r.RLock()
	defer r.RUnlock()
	rec, ok := r.m[id]
	if !ok {
		return rec, ErrRegistryDoesNotExist
	}
	return rec, nil
}
