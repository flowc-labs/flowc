# flowc Architecture Design

## Overview

flowc is an xDS-based API gateway control plane. Resources (Gateways, APIs, Listeners, Deployments, Policies) are kept in a source of truth, projected into an in-process Store, translated into Envoy xDS snapshots, and streamed to Envoy data planes over gRPC.

This document covers the four supported deployment topologies, the source-of-truth pattern, and the resource ownership model.

flowc is packaged as a **single binary**. REST API, xDS server, and the (optional) K8s CRD controller all run in the same process and share the same in-process Store. The four modes differ only in configuration — which source of truth is used, whether the CRD controller is enabled, and whether leader election is on.

---

## Core Abstractions

### Source of Truth vs. Store

Every mode has a clear split:

- **Source of truth** — the authoritative, durable state. Writes go here.
- **Store** — an in-process, in-memory projection of the source of truth. Reads come from here.

```
REST handlers ──write──▶ Source of Truth
                              │
                              │ watch event (informer / LISTEN·NOTIFY / etcd watch / direct)
                              ▼
                         Store (projection)
                              │
                              │ Get / List / Watch
                              ▼
                         xDS reconciler + other consumers
```

| Mode | Source of truth | Watch mechanism |
|---|---|---|
| 1 | in-process map (same object as the Store) | direct emit |
| 2 | Postgres or etcd | `LISTEN/NOTIFY` or etcd watch |
| 3 / 4 | K8s API server (CRDs) | informers |

REST handlers never short-circuit the source of truth. Every write goes to the authoritative store and comes back through the watch path — this is what keeps multi-replica deployments consistent without inter-replica coordination.

In Mode 1, the source of truth and the Store are the same object, so the two steps collapse into a single in-memory write that emits a watch event synchronously.

### Store Interface

A single interface, same shape across all modes:

```go
type Store interface {
    Get(ctx context.Context, key ResourceKey) (*StoredResource, error)
    List(ctx context.Context, filter ListFilter) ([]*StoredResource, error)
    Watch(ctx context.Context, filter WatchFilter) (<-chan WatchEvent, error)
    Put(ctx context.Context, res *StoredResource, opts PutOptions) (*StoredResource, error)
    Delete(ctx context.Context, key ResourceKey, opts DeleteOptions) error
}

type PutOptions struct {
    Owner           OwnerType
    ResourceVersion string  // optimistic concurrency
}
```

`Put` and `Delete` route to the source of truth. `Get`, `List`, and `Watch` serve from the in-memory projection. The projection is only updated by the watch mechanism — never by `Put` directly. Callers don't need to know which mode is active; the implementation selected at startup handles the routing.

**Consistency note.** In Modes 2–4, `Put` returns after the source of truth accepts the write, but the local projection lags by informer / notify latency (typically single-digit milliseconds). A caller that immediately does `Get(sameKey)` may see stale state. This is fine for the xDS pipeline (eventually consistent by design); REST handlers that need read-your-writes semantics return the object echoed back from the source of truth rather than re-reading through `Get`.

### xDS Reconciler

Watches the Store and rebuilds Envoy xDS snapshots. Runs on every replica independently.

**Determinism guarantee**: given identical Store state, all replicas produce identical xDS snapshots. This is what enables multi-replica serving without inter-replica coordination.

```
store.Watch() fires
       │
       ▼
fetch affected Gateway + Listeners + APIs + Deployments + Policies
       │
       ▼
translate to Envoy Listeners, Clusters, RouteConfigurations
       │
       ▼
xdsCache.SetSnapshot(gatewayNodeId, snapshot)
       │
       ▼
Envoy receives updated config via open xDS stream
```

### REST API

Writes flow through `store.Put`, which routes to the source of truth. Stateless — any replica can handle any request because state lives in the source of truth and is projected identically into every replica's Store.

### Controller

Opt-in via `controller.enabled`. Only relevant when the source of truth is the K8s API (Modes 3 and 4); disabled in Modes 1 and 2 where there are no CRDs to reconcile.

When enabled, the controller runs informers on flowc CRDs. Informer events feed the Store — the same watch path the xDS reconciler consumes. The controller also performs **write-back** to the K8s API: status subresources, finalizers, and (future) ownership transfer arbitration.

Write-back must be single-writer across replicas. In Mode 3 (single replica) that's trivially the only replica. In Mode 4 (HA) that's the leader, elected via the K8s lease API. Read paths (informer → Store → xDS) run on every replica regardless of leadership.

---

## Deployment Topologies

### Mode 1: Non-K8s, non-HA (Dev / Single Node)

Single binary, no external dependencies. Not HA.

```
┌──────────────────────────────────────┐
│            Single Binary              │
│                                       │
│  REST API ──Put──┐                    │
│                  ▼                    │
│           In-Memory Store             │
│                  │  (source of truth  │
│                  │   and projection)  │
│                  │ Watch              │
│                  ▼                    │
│           xDS Reconciler              │
│                  │                    │
│                  ▼                    │
│             xDS Cache                 │
│                  │                    │
└──────────────────┼────────────────────┘
                   │ gRPC stream
                   ▼
                 Envoy
```

**Properties:**

| Property | Value |
|---|---|
| Source of truth | in-process map (same as Store) |
| Controller | disabled |
| Leader election | not applicable |
| HA | no |
| K8s required | no |
| Use case | local dev, testing, CI |

**Configuration:**

```yaml
store:
  backend: memory

server:
  rest: ":8080"
  xds: ":8081"

controller:
  enabled: false
```

---

### Mode 2: Non-K8s, HA

Multiple replicas behind a load balancer. An external store — PostgreSQL recommended — is the source of truth. Every replica serves REST and xDS; every replica subscribes to the store's change stream (`LISTEN/NOTIFY` for Postgres, watch for etcd) and projects events into its local Store.

```
              REST API / xDS
              ──────────────▶
                             ┌─────────────────┐
                             │  Load Balancer  │
                             └────────┬────────┘
                                      │
              ┌───────────────────────┼────────────────────┐
              ▼                       ▼                    ▼
        ┌──────────┐            ┌──────────┐         ┌──────────┐
        │ flowc #1 │            │ flowc #2 │         │ flowc #3 │
        │ REST     │            │ REST     │         │ REST     │
        │ xDS      │            │ xDS      │         │ xDS      │
        │ Store    │            │ Store    │         │ Store    │
        │ pg listen│            │ pg listen│         │ pg listen│
        └────┬─────┘            └────┬─────┘         └────┬─────┘
             │                        │                    │
             │ writes + listen        │                    │
             └────────────────────────┼────────────────────┘
                                      ▼
                                 PostgreSQL
                              (source of truth)
```

**No leader election.** The store's optimistic concurrency (`ResourceVersion` on `PutOptions`) is the serialization point. Concurrent writers either succeed in serializable order or get a version conflict and retry — no global leader needed. If a future operation requires single-writer semantics (e.g., a background GC job), it can take a Postgres advisory lock or etcd lease per-operation rather than introducing a process-wide leader role.

**Startup sequence per replica:**

1. Connect to Postgres (or etcd).
2. `store.List(allKinds)` — hydrate the projection from current state.
3. Begin `store.Watch()` — receive incremental updates.
4. Start serving REST and xDS.

There is no "catch-up" problem: the projection is built from a full list on startup, and further changes arrive via the watch stream.

**Properties:**

| Property | Value |
|---|---|
| Source of truth | PostgreSQL or etcd |
| Controller | disabled |
| Leader election | not used |
| HA | yes |
| K8s required | no |
| Use case | production without Kubernetes |

**Configuration:**

```yaml
store:
  backend: postgres
  postgres:
    dsn: "postgres://user:pass@postgres-host:5432/flowc?sslmode=require"

server:
  rest: ":8080"
  xds: ":8081"

controller:
  enabled: false
```

---

### Mode 3: Kubernetes, non-HA

Single replica. K8s CRDs are the source of truth. Informers populate the Store; REST writes go to the K8s API. The CRD controller is enabled but leader election is a no-op — there's only one replica.

```
┌────────────┐           ┌─────────────────────────────┐
│ kubectl ───┼──────────▶│        K8s API Server        │◀──┐
│ REST API ──┼──────┐    │             (CRDs)           │   │
└────────────┘      │    └────────────────┬─────────────┘   │
                    │                     │                  │
                    └─────────────────────┤                  │
                                          │ informer watch   │
                                          ▼                  │
                              ┌─────────────────────┐        │
                              │    flowc (single)    │       │
                              │                      │       │
                              │  K8s Informers       │       │
                              │         │            │       │
                              │         ▼            │       │
                              │    Store (proj.)     │       │
                              │         │            │       │
                              │         ▼            │       │
                              │  xDS Reconciler      │       │
                              │  REST API            │       │
                              │  Controller          │───────┘
                              │                      │ write-back
                              └──────────┬───────────┘ (status,
                                         │            finalizers)
                                         │ xDS stream
                                         ▼
                                       Envoy
```

**Properties:**

| Property | Value |
|---|---|
| Source of truth | K8s API (CRDs) |
| Controller | enabled |
| Leader election | disabled (single replica) |
| HA | no |
| K8s required | yes |
| Use case | small K8s deployments, edge clusters, dev on K8s |

**Configuration:**

```yaml
store:
  backend: kubernetes

server:
  rest: ":8080"
  xds: ":8081"

controller:
  enabled: true

leaderElection:
  enabled: false
```

---

### Mode 4: Kubernetes, HA (Istiod pattern)

Multiple replicas. K8s CRDs are the source of truth. Every replica runs its own informers, builds its own Store projection, and serves REST and xDS. One replica is leader and handles write-back duties: CRD `.status`, finalizers, and ownership transfer arbitration.

```
┌────────────┐           ┌─────────────────────────────┐
│ kubectl ───┼──────────▶│        K8s API Server        │◀──┐
│ REST API ──┼──────┐    │             (CRDs)           │   │
└────────────┘      │    └────────────────┬─────────────┘   │
                    │                     │                  │
                    └─────────────────────┤                  │
                                          │ informer watch   │
                     ┌────────────────────┼───────────────────┐
                     ▼                    ▼                   ▼
               ┌──────────┐         ┌──────────┐        ┌──────────┐
               │ flowc #1 │         │ flowc #2 │        │ flowc #3 │
               │ Informers│         │ Informers│        │ Informers│
               │ Store    │         │ Store    │        │ Store    │
               │ xDS      │         │ xDS      │        │ xDS      │
               │ REST     │         │ REST     │        │ REST     │
               │ Ctrl [L] │         │ Ctrl     │        │ Ctrl     │
               └────┬─────┘         └──────────┘        └──────────┘
                    │
                    │ write-back (leader only)
                    └──────────────────────────────────────────┐
                                                                │
                                                                ▼
                                                         K8s API Server
```

**Responsibility split:**

| Concern | All replicas | Leader only |
|---|---|---|
| Serve REST API | ✓ | |
| Serve xDS | ✓ | |
| Run K8s informers | ✓ | |
| Build Store projection | ✓ | |
| Rebuild xDS snapshots | ✓ | |
| Write CRD `.status` subresource | | ✓ |
| Process finalizers | | ✓ |
| Ownership transfer arbitration | | ✓ |

Losing the leader causes a brief pause in status writes and finalizer processing. REST API and xDS serving continue uninterrupted on all remaining replicas.

**Properties:**

| Property | Value |
|---|---|
| Source of truth | K8s API (CRDs) |
| Controller | enabled |
| Leader election | enabled (write-back only) |
| HA | yes |
| K8s required | yes |
| Use case | production on Kubernetes |

**Configuration:**

```yaml
store:
  backend: kubernetes

server:
  rest: ":8080"
  xds: ":8081"

controller:
  enabled: true

leaderElection:
  enabled: true
  backend: kubernetes
  namespace: flowc-system
  leaseName: flowc-controller
  leaseDuration: 15s
  renewDeadline: 10s
  retryPeriod: 2s
```

---

## Topology Comparison

| | Mode 1 | Mode 2 | Mode 3 | Mode 4 |
|---|---|---|---|---|
| Source of truth | in-memory | Postgres / etcd | K8s CRDs | K8s CRDs |
| REST API | single instance | all replicas | single instance | all replicas |
| xDS serving | single instance | all replicas | single instance | all replicas |
| xDS rebuild trigger | direct emit | watch stream | informer | informer |
| Controller | disabled | disabled | enabled | enabled |
| Leader election | — | — | — | enabled (write-back) |
| HA | no | yes | no | yes |
| K8s required | no | no | yes | yes |
| Extra infrastructure | none | Postgres / etcd | K8s cluster | K8s cluster |

---

## Store Backends

The store backend is selected via configuration. The `Store` interface is the only dependency — the rest of the codebase (REST handlers, xDS reconciler, controller) is backend-agnostic.

| Backend | Source of truth | Watch mechanism | Use case |
|---|---|---|---|
| `memory` | in-process map | direct emit | Mode 1 — dev, testing, single node |
| `postgres` | PostgreSQL | `LISTEN/NOTIFY` | Mode 2 default — widely available, familiar to ops |
| `etcd` | etcd | native gRPC watch stream | Mode 2 alternative — sub-millisecond watch latency |
| `kubernetes` | K8s API | informers | Modes 3 / 4 |
| `mysql` | MySQL | polling | only if MySQL is already a hard operational constraint |

**PostgreSQL** is the recommended backend for non-K8s production. Full ACID transactions, `LISTEN/NOTIFY` for efficient change streaming, and available as a managed service on every cloud (RDS, Cloud SQL, Supabase, Neon).

`LISTEN/NOTIFY` feeds the Store projection:

```
store.Put() writes row
      │
      ▼
trigger fires → pg_notify('flowc_resources', payload)
      │
      ▼
subscribing goroutine receives notification
      │
      ▼
update in-memory projection → emit WatchEvent
      │
      ▼
xDS reconciler rebuilds snapshot
```

**etcd** is a good choice if you need the lowest possible watch latency, or if you're deploying alongside an existing etcd cluster you want to reuse.

**MySQL** lacks a native pub/sub equivalent to `LISTEN/NOTIFY`. Watch falls back to polling, which adds latency proportional to the poll interval. Prefer PostgreSQL unless MySQL is already a hard operational constraint.

---

## Resource Ownership Model

Ownership matters when resources can be created through more than one channel — REST API versus K8s CRDs, or (future) a GitOps controller. In Modes 1 and 2 there is only one owner (`flowc-control-plane`) and ownership transfer is a no-op. Everything in this section applies to Modes 3 and 4.

### Owner Types

| Owner | Set by | Write path |
|---|---|---|
| `flowc-control-plane` | flowc REST API | `PUT /api/v1/{kind}/{name}` |
| `kubernetes` | K8s controller | CRD watch → `store.Put()` |
| `gitops` | future (Flux / Argo CD) | SSA field manager detection |

### Management Policy (per resource)

Controls what the owning controller does with the resource.

| Policy | Behaviour |
|---|---|
| `FullControl` | Owner has full CRUD authority. Writes from other owners are rejected. Controller reconciles and reverts drift. |
| `ObserveOnly` | No writes from any owner. Resource is read-only; used for inspection without management. |
| `Paused` | Owner retains authority but the reconciler does not sync changes to xDS. Used for maintenance windows. |

### Transfer Policy (per resource, with global default)

Controls whether ownership can move between owners.

| Policy | Behaviour |
|---|---|
| `Locked` | Ownership cannot be transferred. |
| `Manual` | Transfer requires an explicit API request. |
| `Auto` | Any owner can claim the resource without a prior request. |

### Ownership Fields

Every resource carries ownership metadata in spec (desired) and status (actual). The spec/status split means a transfer request is visible and auditable before it completes.

```go
type OwnershipSpec struct {
    Owner            OwnerType        `json:"owner"`
    TransferPolicy   TransferPolicy   `json:"transferPolicy,omitempty"`
    ManagementPolicy ManagementPolicy `json:"managementPolicy,omitempty"`
}

type OwnershipStatus struct {
    ActiveOwner   OwnerType   `json:"activeOwner"`
    PreviousOwner OwnerType   `json:"previousOwner,omitempty"`
    PendingOwner  OwnerType   `json:"pendingOwner,omitempty"`  // set during transfer
    TransferredAt metav1.Time `json:"transferredAt,omitempty"`
    TransferredBy string      `json:"transferredBy,omitempty"`
}
```

### Global Config

```yaml
apiVersion: flowc.io/v1alpha1
kind: FlowcConfig
metadata:
  name: cluster   # singleton
spec:
  ownership:
    defaultOwner: flowc-control-plane
    defaultTransferPolicy: Locked
    kinds:
      - kind: Gateway
        transferable: true
        defaultTransferPolicy: Manual
      - kind: API
        transferable: true
        defaultTransferPolicy: Manual
      - kind: Listener
        transferable: false   # omitted = not transferable
      - kind: Deployment
        transferable: true
        defaultTransferPolicy: Manual
```

---

## Ownership Transfer

Transfer is a two-phase operation. `pendingOwner` is set first; the transfer completes only when the incoming owner successfully writes to the store. This prevents a window where both sides believe they hold authority.

### State Machine

```
                   transfer requested
  flowc-cp ──────────────────────────▶ flowc-cp [pending: k8s]
                                                │
                                  k8s controller confirms
                                  (store.Put owner="kubernetes")
                                                │
  kubernetes ◀────────────────────────────────────
       │
       │  transfer requested
       ▼
  kubernetes [pending: flowc-cp]
       │
       │  REST API confirms + controller stops writing
       ▼
  flowc-cp
```

### Scenario A: flowc-control-plane → kubernetes (explicit transfer)

Operator decides to hand a flowc-managed resource to Kubernetes management:

```
1. POST /api/v1/gateways/my-gw/transfer
   Body: {"to": "kubernetes"}

2. Store sets pendingOwner: "kubernetes"
   (activeOwner remains "flowc-control-plane" — still authoritative)

3. flowc writes current spec to K8s CRD:
   - Creates or updates Gateway CRD in K8s
   - Sets annotation: flowc.io/owner: kubernetes

4. K8s controller picks up CRD via watch
   Calls store.Put(owner="kubernetes")

5. Store: pendingOwner == proposed owner
   → activeOwner: "kubernetes"
   → pendingOwner cleared
   → previousOwner: "flowc-control-plane"

6. REST API writes to my-gw now return ErrOwnershipConflict
```

### Scenario B: kubernetes-initiated (user creates CRD directly)

User runs `kubectl apply` for a resource that already exists in the store:

```
1. kubectl apply -f gateway.yaml
   (annotation: flowc.io/owner: kubernetes)

2. K8s controller reconciles → store.Put(owner="kubernetes")

3. Store checks existing activeOwner: "flowc-control-plane"
   Looks up transfer policy for kind=Gateway in FlowcConfig:

   Locked  → ErrOwnershipConflict
              controller sets status condition: OwnershipConflict=True
              no change

   Manual  → same as Locked (explicit request required first)

   Auto    → transfer completes immediately
              activeOwner: "kubernetes"
              REST API writes now rejected
```

### Scenario C: kubernetes → flowc-control-plane

```
1. POST /api/v1/gateways/my-gw/transfer
   Body: {"to": "flowc-control-plane"}

2. Store sets pendingOwner: "flowc-control-plane"

3. flowc patches K8s CRD annotation:
   flowc.io/owner: flowc-control-plane

4. K8s controller reconciles annotation change
   Sees owner != "kubernetes" → switches to observe-only mode
   Stops calling store.Put() for spec changes

5. REST API calls store.Put(owner="flowc-control-plane")
   pendingOwner matches → transfer complete
   activeOwner: "flowc-control-plane"

6. K8s controller only updates status going forward, never spec
```

### Controller Reconcile with Ownership

```go
func (r *GatewayReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    var gw flowcv1alpha1.Gateway
    if err := r.k8sClient.Get(ctx, req.NamespacedName, &gw); err != nil {
        return reconcile.Result{}, client.IgnoreNotFound(err)
    }

    if !gw.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, &gw)
    }

    // Resource annotated for flowc ownership — observe only, don't write spec
    if gw.Annotations["flowc.io/owner"] != "kubernetes" {
        return r.updateStatusOnly(ctx, &gw)
    }

    err := r.store.Put(ctx, toStoreResource(&gw), store.PutOptions{
        Owner: OwnerKubernetes,
    })

    switch {
    case errors.Is(err, store.ErrOwnershipConflict):
        // flowc-control-plane owns this, transfer policy blocked auto-takeover
        return r.setConflictStatus(ctx, &gw, err)

    case errors.Is(err, store.ErrTransferPending):
        // Transfer initiated, waiting for incoming owner to confirm
        return reconcile.Result{RequeueAfter: 2 * time.Second}, nil
    }

    return reconcile.Result{}, err
}
```

---

## Store Enforcement

The store is the single enforcement point for ownership. No other layer needs to check — callers simply get a typed error back.

```go
var (
    ErrOwnershipConflict = errors.New("resource owned by another owner")
    ErrTransferPending   = errors.New("ownership transfer in progress")
    ErrTransferNotAllowed = errors.New("transfer policy does not permit this transfer")
)
```

The existing `ConflictPolicy` (strict / warn / takeover) maps directly onto the transfer policy model:

| Existing | New |
|---|---|
| `ConflictStrict` | `TransferLocked` + `ManagementPolicy: FullControl` |
| `ConflictWarn` | `TransferManual` |
| `ConflictTakeover` | `TransferAuto` |

The `ManagedBy` annotation (`flowc.io/managed-by`) becomes the `activeOwner` status field. The `X-Managed-By` HTTP header maps to `PutOptions.Owner`.
