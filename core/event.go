package core

// EventEmitter allows services to emit events without depending on
// Wails or CLI-specific transport mechanisms.
type EventEmitter interface {
	Emit(event string, data interface{})
}

// NilEventEmitter silently discards all events. Used as default when
// no emitter is configured.
type NilEventEmitter struct{}

func (NilEventEmitter) Emit(event string, data interface{}) {}
