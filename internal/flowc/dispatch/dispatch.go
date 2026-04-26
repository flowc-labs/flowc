// Package dispatch routes store-watch events to per-kind xDS translators
// with debouncing.
//
// Two translators today: a Gateway translator does full snapshot
// rebuilds; a Deployment translator does surgical per-deployment
// upserts/removes. Translators consume the in-memory indexer for
// dependent-resource lookups and write to the xDS ConfigManager
// directly. The dispatcher itself holds no domain knowledge — it just
// debounces, deduplicates by (Kind, Name), and routes.
package dispatch

import (
	"context"
	"sync"
	"time"

	"github.com/flowc-labs/flowc/internal/flowc/index"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// DefaultDebounce is the standard window for coalescing related events.
// Matches the previous reconciler's window so behavior under load is
// comparable.
const DefaultDebounce = 100 * time.Millisecond

// Translator produces and publishes xDS resources for one kind of owner
// (Gateway or Deployment today). Each translator is constructed with
// references to the indexer and cache it needs and is registered against
// a kind name; the dispatcher routes tasks to it by Kind().
//
// task.Deletion distinguishes upsert/remove modes. For deletions, the
// owner resource is no longer in the indexer; translators must rely on
// the indexer's ownership map (or other recorded state) to act.
type Translator interface {
	Kind() string
	Translate(ctx context.Context, task index.AffectedTask) error
}

// Dispatcher accumulates AffectedTasks from the indexer's Apply, coalesces
// duplicates within a debounce window, and runs the matching translator
// for each unique (Kind, Name) pair. Last-write-wins on Deletion: if a
// resource is Put then Deleted within the window, the Delete action runs.
type Dispatcher struct {
	translators map[string]Translator
	debounce    time.Duration
	log         *logger.EnvoyLogger

	mu      sync.Mutex
	pending map[taskKey]index.AffectedTask
	timer   *time.Timer
}

type taskKey struct {
	Kind string
	Name string
}

// New constructs a Dispatcher. Pass DefaultDebounce unless tests need
// something tighter.
func New(debounce time.Duration, log *logger.EnvoyLogger) *Dispatcher {
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	return &Dispatcher{
		translators: make(map[string]Translator),
		debounce:    debounce,
		log:         log,
		pending:     make(map[taskKey]index.AffectedTask),
	}
}

// Register installs a translator for a kind. Panics on double-registration
// — translator wiring should be deterministic at startup, so a duplicate
// is a bug worth crashing on.
func (d *Dispatcher) Register(t Translator) {
	if _, exists := d.translators[t.Kind()]; exists {
		panic("dispatch: translator already registered for kind " + t.Kind())
	}
	d.translators[t.Kind()] = t
}

// Enqueue accepts a batch of tasks (typically the result of one
// indexer.Apply call) and schedules a debounced flush. Subsequent
// Enqueues within the debounce window extend it; the flush only fires
// when the window has been quiet for the full debounce duration.
//
// ctx is captured for the eventual flush; pass the long-lived event-loop
// context so cancellation propagates to translators.
func (d *Dispatcher) Enqueue(ctx context.Context, tasks []index.AffectedTask) {
	if len(tasks) == 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, t := range tasks {
		d.pending[taskKey{t.Kind, t.Name}] = t
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.debounce, func() {
		d.Flush(ctx)
	})
}

// Flush runs all pending translations immediately. Exposed for the
// startup full-rebuild path (where we want translation without waiting
// for the debounce window) and for tests.
func (d *Dispatcher) Flush(ctx context.Context) {
	d.mu.Lock()
	pending := d.pending
	d.pending = make(map[taskKey]index.AffectedTask)
	d.timer = nil
	d.mu.Unlock()

	if len(pending) == 0 {
		return
	}

	for _, task := range pending {
		translator, ok := d.translators[task.Kind]
		if !ok {
			if d.log != nil {
				d.log.WithFields(map[string]any{
					"kind": task.Kind,
					"name": task.Name,
				}).Warn("Dispatcher: no translator registered for kind")
			}
			continue
		}
		if err := translator.Translate(ctx, task); err != nil {
			if d.log != nil {
				d.log.WithFields(map[string]any{
					"kind":     task.Kind,
					"name":     task.Name,
					"deletion": task.Deletion,
					"error":    err.Error(),
				}).Error("Translation failed")
			}
		}
	}
}
