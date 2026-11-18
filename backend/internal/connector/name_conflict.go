package connector

import (
	"context"
	"errors"
)

// NameConflict contains public recovery information only, never authorization credentials.
type NameConflict struct {
	ObjectType    string `json:"objectType,omitempty"`
	DashboardURL  string `json:"dashboardUrl,omitempty"`
	Reason        string `json:"reason,omitempty"`
	SuggestedName string `json:"suggestedName,omitempty"`
	ConflictName  string `json:"conflictName,omitempty"`
	DNSURL        string `json:"dnsUrl,omitempty"`
	TunnelURL     string `json:"tunnelUrl,omitempty"`
	ReuseRecord   string `json:"reuseRecord,omitempty"`
}
type nameConflictError struct {
	cause   error
	details NameConflict
}

func (e *nameConflictError) Error() string             { return e.cause.Error() }
func (e *nameConflictError) Unwrap() error             { return e.cause }
func WithNameConflict(err error, d NameConflict) error { return &nameConflictError{err, d} }
func Conflict(err error) *NameConflict {
	var e *nameConflictError
	if errors.As(err, &e) {
		d := e.details
		return &d
	}
	return nil
}

// NameReuse is an explicit administrator click bound to one observed DNS record.
type NameReuse struct {
	Name   string `json:"name"`
	Record string `json:"record"`
}
type nameReuseKey struct{}

func WithNameReuse(ctx context.Context, reuse *NameReuse) context.Context {
	existing, _ := ctx.Value(nameReuseKey{}).(map[string]*NameReuse)
	next := map[string]*NameReuse{}
	for k, v := range existing {
		next[k] = v
	}
	if reuse != nil {
		next[reuse.Name] = reuse
	}
	return context.WithValue(ctx, nameReuseKey{}, next)
}
func ReuseFor(ctx context.Context, name string) *NameReuse {
	m, _ := ctx.Value(nameReuseKey{}).(map[string]*NameReuse)
	return m[name]
}
func ReuseName(ctx context.Context) *NameReuse {
	m, _ := ctx.Value(nameReuseKey{}).(map[string]*NameReuse)
	for _, v := range m {
		return v
	}
	return nil
}

type anotherNameKey struct{}

// WithAnotherName is an administrator's request to choose a fresh free name,
// never permission to replace a conflicting object.
func WithAnotherName(ctx context.Context) context.Context {
	return context.WithValue(ctx, anotherNameKey{}, true)
}
func AnotherName(ctx context.Context) bool {
	value, _ := ctx.Value(anotherNameKey{}).(bool)
	return value
}
