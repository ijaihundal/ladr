// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package provider

import (
	"fmt"
	"sort"
	"sync"
)

var (
	adapters       = make(map[string]ProviderAdapter)
	adaptersMu     sync.RWMutex
	customFallback ProviderAdapter
)

// RegisterAdapter adds an adapter to the global registry.
func RegisterAdapter(adapter ProviderAdapter) {
	adaptersMu.Lock()
	defer adaptersMu.Unlock()
	adapters[adapter.Name()] = adapter
	if adapter.Name() == "custom" {
		customFallback = adapter
	}
}

// GetAdapter returns the adapter for the given name. If the name is not
// registered but is a custom provider ID, the custom fallback adapter is
// returned. Returns an error if no adapter is found.
func GetAdapter(name string) (ProviderAdapter, error) {
	adaptersMu.RLock()
	adapter, ok := adapters[name]
	if ok {
		adaptersMu.RUnlock()
		return adapter, nil
	}
	if customFallback != nil && IsCustomProviderID(name) {
		adaptersMu.RUnlock()
		return customFallback, nil
	}
	adaptersMu.RUnlock()
	return nil, fmt.Errorf("Provider adapter not registered: %s", name)
}

// ListAdapters returns a sorted list of registered adapter names.
func ListAdapters() []string {
	adaptersMu.RLock()
	defer adaptersMu.RUnlock()
	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
