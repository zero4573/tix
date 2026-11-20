#!/usr/bin/env bash
set -euo pipefail
mkdir -p retry web
cat > go.mod <<'G'
module example.com/retry

go 1.23
G
cat > retry/retry.go <<'G'
package retry

import "time"

func Do(attempts int, wait time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(wait)
	}
	return err
}
G
cat > web/format.ts <<'G'
export function formatCount(n: number): string {
  return n === 1 ? "1 item" : `${n} items`;
}
G
