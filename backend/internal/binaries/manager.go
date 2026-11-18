package binaries

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrBusy = errors.New("binary operation already in progress")

// ErrNoPrevious refuses a rollback when no earlier verified version is kept.
var ErrNoPrevious = errors.New("previous version unavailable")

type Status struct {
	RuntimeVersion  string `json:"runtimeVersion"`
	Busy            bool   `json:"busy"`
	Product         string `json:"product"`
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	Previous        string `json:"previous"`
	CheckedAt       string `json:"checkedAt"`
	Verification    string `json:"verification"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Phase           string `json:"phase"`
	Error           string `json:"error"`
	ErrorCode       string `json:"errorCode,omitempty"`
	RetryAt         string `json:"retryAt,omitempty"`
	// Hint explains a GitHub refusal in plain words for the CLI; the page
	// builds its own translated text from ErrorCode and RetryAt.
	Hint        string `json:"hint,omitempty"`
	CurrentDir  string `json:"-"`
	PreviousDir string `json:"-"`
	High        string `json:"-"`
}
type saved struct {
	Digests         map[string]string
	PreviousDigests map[string]string
	Status
	CurrentDir  string
	PreviousDir string
	High        string
}
type Manager struct {
	root   string
	source Source
}

func New(root string, source Source) *Manager { return &Manager{root: root, source: source} }
func (m *Manager) Status(product string) (Status, error) {
	s, e := m.read(product)
	if e != nil {
		return Status{}, e
	}
	// A killed CGI releases its OS lock. Re-read under that lock before
	// describing an abandoned progress record as interrupted.
	if s.Phase == "checking" || s.Phase == "downloading" || s.Phase == "verifying" {
		_ = m.locked(context.Background(), func() error {
			fresh, err := m.read(product)
			if err != nil {
				return err
			}
			s = fresh
			if s.Phase == "checking" || s.Phase == "downloading" || s.Phase == "verifying" {
				s.Phase = "failed"
				s.Error = "binary operation interrupted; retry"
			}
			return nil
		})
	}
	out := s.Status
	out.Hint = hint(out.ErrorCode, out.RetryAt)
	if out.Phase == "idle" {
		out.Phase = restingPhase(out)
	}
	out.CurrentDir = s.CurrentDir
	out.PreviousDir = s.PreviousDir
	out.High = s.High
	if out.Latest != "" {
		n, _ := compare(out.Latest, out.Current)
		out.UpdateAvailable = out.Current == "" || n > 0
	}
	return out, nil
}
func (m *Manager) read(p string) (saved, error) {
	s := saved{Status: Status{Product: p, Phase: "idle"}}
	if e := validProduct(p); e != nil {
		return s, e
	}
	b, e := os.ReadFile(filepath.Join(m.root, p+".json"))
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e != nil {
		return s, e
	}
	if len(b) > 16384 {
		return s, errors.New("invalid binary state")
	}
	e = json.Unmarshal(b, &s)
	return s, e
}
func atomicJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".state-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}

// fail records err, naming a GitHub refusal so the page and the CLI can
// explain it and point to the GitHub token.
func (s *saved) fail(err error) {
	s.Error, s.ErrorCode, s.RetryAt = err.Error(), "", ""
	var refused *SourceError
	if errors.As(err, &refused) {
		s.ErrorCode = refused.Code
		if !refused.RetryAt.IsZero() {
			s.RetryAt = refused.RetryAt.UTC().Format(time.RFC3339)
		}
	}
}
func (m *Manager) save(s saved) error { return atomicJSON(filepath.Join(m.root, s.Product+".json"), s) }
func (m *Manager) locked(ctx context.Context, fn func() error) error {
	if e := os.MkdirAll(m.root, 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(m.root, "operation.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		if errors.Is(e, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		return e
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if e = ctx.Err(); e != nil {
		return e
	}
	return fn()
}
func (m *Manager) Check(ctx context.Context, p string) error {
	return m.locked(ctx, func() error { _, e := m.check(ctx, p); return e })
}
func (m *Manager) check(ctx context.Context, p string) (Release, error) {
	s, e := m.read(p)
	if e != nil {
		return Release{}, e
	}
	s.Phase = "checking"
	s.Error, s.ErrorCode, s.RetryAt = "", "", ""
	if e = m.save(s); e != nil {
		return Release{}, e
	}
	r, e := m.source.Latest(ctx, p)
	s.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	s.Phase = restingPhase(s.Status)
	if e != nil {
		s.fail(e)
	} else {
		s.Latest = r.Version
	}
	if se := m.save(s); se != nil {
		return r, se
	}
	return r, e
}
func (m *Manager) Update(ctx context.Context, p string) error {
	return m.locked(ctx, func() error {
		r, e := m.check(ctx, p)
		if e != nil {
			return e
		}
		return m.installLocked(ctx, r)
	})
}
func (m *Manager) installLocked(ctx context.Context, r Release) (err error) {
	s, e := m.read(r.Product)
	if e != nil {
		return e
	}
	if _, e = compare(r.Version, r.Version); e != nil {
		return e
	}
	if s.High != "" {
		n, e := compare(r.Version, s.High)
		if e != nil {
			return e
		}
		if n < 0 {
			return errors.New("release downgrade rejected")
		}
		if n == 0 && s.Current == r.Version {
			return nil
		}
	}
	s.Phase = "downloading"
	s.Error, s.ErrorCode, s.RetryAt = "", "", ""
	if e = m.save(s); e != nil {
		return e
	}
	defer func() {
		if err != nil {
			s.Phase = "failed"
			s.fail(err)
			_ = m.save(s)
		}
	}()
	stage, e := os.MkdirTemp(m.root, ".candidate-*")
	if e != nil {
		return e
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(stage)
		}
	}()
	payload := filepath.Join(stage, "download")
	verification, e := m.source.Download(ctx, r, payload)
	if e != nil {
		return e
	}
	s.Phase = "verifying"
	if e = m.save(s); e != nil {
		return e
	}
	if r.Product == "tailscale" || r.Product == "netbird" || r.Product == "netbird-server" {
		f, e := os.Open(payload)
		if e != nil {
			return e
		}
		if r.Product != "tailscale" {
			e = extract(f, stage, 512<<20, r.Product)
		} else {
			e = extract(f, stage, 512<<20)
		}
		f.Close()
		if e != nil {
			return e
		}
		if e = os.Remove(payload); e != nil {
			return e
		}
	} else {
		if e = os.Rename(payload, filepath.Join(stage, "cloudflared")); e != nil {
			return e
		}
		if e = os.Chmod(filepath.Join(stage, "cloudflared"), 0700); e != nil {
			return e
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = syncDirectory(stage); e != nil {
		return e
	}
	if e = syncDirectory(m.root); e != nil {
		return e
	}
	digests := map[string]string{}
	names := []string{r.Product}
	if r.Product == "tailscale" {
		names = append(names, "tailscaled")
	}
	for _, name := range names {
		if c, ok := m.source.(executableChecker); ok {
			if e = c.CheckExecutable(filepath.Join(stage, name)); e != nil {
				return e
			}
		}
		digest, e := fileDigest(ctx, filepath.Join(stage, name))
		if e != nil {
			return e
		}
		digests[name] = digest
	}
	old := s
	s.PreviousDigests = s.Digests
	s.Digests = digests
	s.Previous = s.Current
	s.PreviousDir = s.CurrentDir
	s.Current = r.Version
	s.CurrentDir = filepath.Base(stage)
	s.High = r.Version
	s.Verification = verification
	s.Phase = "ready"
	s.Error = ""
	if e = m.save(s); e != nil {
		s = old
		return e
	}
	keep = true
	// Retired directories may still be pinned by a provider. Reclamation belongs
	// to the provider-aware lifecycle, not a download operation.

	return nil
}
func (m *Manager) directory(name string) (string, error) {
	if name == "" || filepath.Base(name) != name || len(name) < 11 || name[:11] != ".candidate-" {
		return "", errors.New("invalid candidate directory")
	}
	return filepath.Join(m.root, name), nil
}

// Paths resolves a complete verified candidate, never a download in staging.
func (m *Manager) Paths(p string) (map[string]string, error) {
	s, e := m.read(p)
	if e != nil {
		return nil, e
	}
	dir, e := m.directory(s.CurrentDir)
	if e != nil {
		return nil, e
	}
	names := []string{p}
	if p == "tailscale" {
		names = append(names, "tailscaled")
	}
	out := map[string]string{}
	for _, n := range names {
		path := filepath.Join(dir, n)
		digest, e := fileDigest(context.Background(), path)
		if e != nil || digest != s.Digests[n] {
			return nil, errors.New("installed binary verification failed")
		}
		out[n] = path
	}
	return out, nil
}

// Activate delegates stop/start/readiness/restore decisions to the provider. A
// download is a candidate, not evidence that the running process was upgraded.
func (m *Manager) Activate(ctx context.Context, p string, hook func(context.Context, map[string]string) error) error {
	return m.locked(ctx, func() error {
		if hook == nil {
			return errors.New("provider activation unavailable")
		}
		paths, e := m.Paths(p)
		if e != nil {
			return e
		}
		return hook(ctx, paths)
	})
}
func (m *Manager) Rollback(ctx context.Context, p string) error {
	return m.locked(ctx, func() error {
		s, e := m.read(p)
		if e != nil {
			return e
		}
		if s.Previous == "" {
			return ErrNoPrevious
		}
		s.Digests, s.PreviousDigests = s.PreviousDigests, s.Digests
		s.Current, s.Previous = s.Previous, s.Current
		s.CurrentDir, s.PreviousDir = s.PreviousDir, s.CurrentDir
		s.Phase = "ready"
		s.Error, s.ErrorCode, s.RetryAt = "", "", ""
		return m.save(s)
	})
}

func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

func hint(code, retryAt string) string {
	switch code {
	case "github_rate_limited":
		when := "later"
		if retryAt != "" {
			when = "after " + retryAt
		}
		return "GitHub rate limit reached; try again " + when + ". Add a GitHub token in Settings > GitHub token to raise the limit"
	case "github_token_invalid":
		return "GitHub rejected the saved token because it is invalid or expired. Replace or remove it in Settings > GitHub token"
	}
	return ""
}

// restingPhase names a binary that is not busy: ready once a version is installed.
func restingPhase(s Status) string {
	if s.Current != "" {
		return "ready"
	}
	return "idle"
}
