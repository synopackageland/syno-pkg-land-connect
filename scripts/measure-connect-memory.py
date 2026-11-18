#!/usr/bin/env python3
"""Measure idle production RSS/PSS and separate instrumented Go runtime stats.

Run on Linux from a checkout with the pinned Go toolchain (CGO_ENABLED=0).
No provider is enabled; all state/binaries live in a temporary directory.
The probe is a disposable Go overlay, never part of the shipped executables.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time

PROBE = '''package main
import ("encoding/json"; "os"; "os/signal"; "runtime"; "syscall")
func init() {
 if dir := os.Getenv("CONNECT_MEMORY_PROBE_DIR"); dir != "" {
  ch := make(chan os.Signal, 1)
  signal.Notify(ch, syscall.SIGUSR1)
  go func() { for range ch {
   var m runtime.MemStats
   runtime.ReadMemStats(&m)
   b, _ := json.Marshal(map[string]any{"go": runtime.Version(), "goroutines": runtime.NumGoroutine()-1, "heap_alloc": m.HeapAlloc, "heap_inuse": m.HeapInuse, "heap_sys": m.HeapSys, "heap_idle": m.HeapIdle, "heap_released": m.HeapReleased, "stack_inuse": m.StackInuse, "sys": m.Sys, "total_alloc": m.TotalAlloc, "mallocs": m.Mallocs, "gc": m.NumGC})
   name := dir+"/"+__itoa(os.Getpid())
   _ = os.WriteFile(name+".json", b, 0600)
   stack := make([]byte, 65536)
   n := runtime.Stack(stack, true)
   _ = os.WriteFile(name+".stacks", stack[:n], 0600)
  }}()
 }
}
func __itoa(n int) string { if n == 0 { return "0" }; b := [20]byte{}; i:=len(b); for n>0 {i--;b[i]=byte('0'+n%10);n/=10};return string(b[i:]) }
'''


def run(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs)


def fields(path):
    result = {}
    for line in path.read_text().splitlines():
        key, _, value = line.partition(':')
        bits = value.split()
        if bits and bits[0].isdigit():
            result[key] = int(bits[0])
    return result


def children_of(pid):
    return sorted({int(child) for task in Path(f'/proc/{pid}/task').iterdir()
                   for child in (task/'children').read_text().split()})


def snapshot(pid):
    status = fields(Path(f'/proc/{pid}/status'))
    maps = fields(Path(f'/proc/{pid}/smaps_rollup'))
    return {**{k: status[k] for k in ['VmRSS', 'RssAnon', 'RssFile', 'Threads']},
            **{k: maps[k] for k in ['Rss', 'Pss', 'Private_Dirty', 'Shared_Clean']}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--runs', type=int, default=3)
    parser.add_argument('--settle', type=float, default=3)
    args = parser.parse_args()
    backend = Path(__file__).resolve().parent.parent / 'backend'
    args.output = args.output.resolve()
    if args.runs < 1 or args.settle < 0:
        parser.error('--runs must be positive and --settle nonnegative')
    os.chdir(backend)
    os.environ['CGO_ENABLED'] = '0'
    args.output.mkdir(parents=True, exist_ok=True)
    result = {'go': run('go', 'version').strip(), 'rss_units': 'KiB', 'runtime_units': 'bytes',
              'gc_env': {k: os.environ[k] for k in ['GOGC', 'GOMEMLIMIT', 'GOMAXPROCS'] if k in os.environ},
              'settle_seconds': args.settle, 'production': [], 'instrumented': []}
    commands = ['connectd'] + (['connect-worker'] if (backend/'cmd/connect-worker').is_dir() else [])
    with tempfile.TemporaryDirectory(prefix='connect-memory-') as tmp:
        work = Path(tmp)
        for mode in ['production', 'instrumented']:
            bindir = work/mode
            bindir.mkdir()
            for command in commands:
                build = ['go', 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w']
                if mode == 'instrumented':
                    probe = bindir/f'{command}-probe.go'
                    probe.write_text(PROBE)
                    overlay = bindir/f'{command}-overlay.json'
                    overlay.write_text(json.dumps({'Replace': {str(backend/f'cmd/{command}/zz_memory_probe.go'): str(probe)}}))
                    build += ['-overlay='+str(overlay)]
                subprocess.run([*build, '-o', str(bindir/command), './cmd/'+command], check=True)
                if mode == 'production':
                    deps = run('go', 'list', '-deps', './cmd/'+command).splitlines()
                    result[command] = {'binary_bytes': (bindir/command).stat().st_size, 'packages': len(deps)}
                    (args.output/f'{command}-deps.txt').write_text('\n'.join(deps)+'\n')
            for attempt in range(args.runs if mode == 'production' else 1):
                state = bindir/f'state-{attempt}'
                state.mkdir()
                env = dict(os.environ)
                if mode == 'instrumented':
                    env['CONNECT_MEMORY_PROBE_DIR'] = str(bindir)
                with (args.output/f'{mode}-{attempt}.log').open('w') as log:
                    parent = subprocess.Popen([str(bindir/'connectd'), 'serve', str(work/'target'), str(state)], env=env, stdout=log, stderr=log)
                    try:
                        # Readiness remains tolerant; this is a fixture bound, not a runtime timeout.
                        deadline = time.monotonic()+60
                        while not (state/'connect/control.sock').exists():
                            if parent.poll() is not None or time.monotonic() > deadline:
                                raise RuntimeError('fixture worker did not start')
                            time.sleep(.05)
                        time.sleep(args.settle)
                        children = children_of(parent.pid)
                        if len(children) != 1:
                            raise RuntimeError(f'expected one worker, got {children}')
                        child = int(children[0])
                        child_children = children_of(child)
                        sample = {'supervisor': snapshot(parent.pid), 'worker': snapshot(child), 'provider_children': child_children}
                        if child_children:
                            raise RuntimeError('idle worker started a provider process')
                        if mode == 'instrumented':
                            for label, pid in [('supervisor', parent.pid), ('worker', child)]:
                                os.kill(pid, signal.SIGUSR1)
                                probe_file = bindir/f'{pid}.json'
                                deadline = time.monotonic()+5
                                while not probe_file.exists():
                                    if time.monotonic() > deadline:
                                        raise RuntimeError('runtime probe did not answer')
                                    time.sleep(.02)
                                sample[label]['runtime'] = json.loads(probe_file.read_text())
                                stack = bindir/f'{pid}.stacks'
                                while not stack.exists() and time.monotonic() < deadline:
                                    time.sleep(.02)
                                (args.output/f'{label}-goroutines.txt').write_text(stack.read_text())
                        result[mode].append(sample)
                    finally:
                        # Use the package controller's normal stop path. A control-socket
                        # shutdown reply can race the worker closing its HTTP listener.
                        parent.send_signal(signal.SIGTERM)
                        try:
                            parent.wait(timeout=30)
                        except subprocess.TimeoutExpired:
                            parent.kill()
                            parent.wait()
                            raise RuntimeError('supervisor did not stop after SIGTERM')
                        if (state/'connect/control.sock').exists():
                            raise RuntimeError('worker left its control socket after SIGTERM')
    (args.output/'measurements.json').write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
