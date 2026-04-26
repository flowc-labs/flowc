/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

// Standard condition types used across all flowc resources. Defined in one
// place so providers, store backends, and consumers agree on the vocabulary.
const (
	// ConditionAccepted means the resource is structurally and semantically
	// valid in isolation: required fields present, embedded specs parse,
	// own constraints satisfied. It does NOT imply that referenced
	// resources (peers) are themselves Ready.
	ConditionAccepted = "Accepted"

	// ConditionReady means ConditionAccepted is True AND every referenced
	// resource is itself Ready. This is the canonical projectability gate:
	// store backends mirror only resources whose Ready condition is True,
	// so xDS consumers receive a stream of Ready resources without doing
	// any status filtering of their own.
	//
	// For resources that compose no references (e.g. Gateway, Listener,
	// API), Ready collapses to the same meaning as Accepted, but is still
	// written so that consumers gate uniformly on a single condition type.
	ConditionReady = "Ready"

	// ConditionProgrammed means the resource has been successfully
	// translated into xDS resources and pushed to the data plane.
	// Reserved: not written by any code today; will be set by the xDS
	// layer once a status writeback path exists. Consumers should not
	// read it yet — it's defined here so the vocabulary is settled.
	ConditionProgrammed = "Programmed"
)
