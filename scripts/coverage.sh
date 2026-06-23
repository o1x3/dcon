#!/usr/bin/env bash
#
# Compute total test coverage and update the coverage badge in README.md.
# Used locally and by CI (which commits the refreshed badge back to main).
#
# The badge lives between markers so updates are deterministic:
#   <!--COVERAGE-->![coverage](...)<!--/COVERAGE-->
#
set -euo pipefail
cd "$(dirname "$0")/.."

go test ./... -coverprofile=coverage.out >/dev/null 2>&1 || true
PCT="$(go tool cover -func=coverage.out 2>/dev/null | awk '/^total:/ {gsub(/%/,"",$3); print $3}')"
PCT="${PCT:-0.0}"

# pick a shields colour by threshold
num="${PCT%.*}"
if   [ "$num" -ge 80 ]; then COLOR="brightgreen"
elif [ "$num" -ge 60 ]; then COLOR="green"
elif [ "$num" -ge 40 ]; then COLOR="yellowgreen"
elif [ "$num" -ge 20 ]; then COLOR="yellow"
else COLOR="orange"; fi

export BADGE="<!--COVERAGE-->![coverage](https://img.shields.io/badge/coverage-${PCT}%25-${COLOR})<!--/COVERAGE-->"

if grep -q '<!--COVERAGE-->' README.md; then
  perl -0pi -e 's{<!--COVERAGE-->.*?<!--/COVERAGE-->}{$ENV{BADGE}}s' README.md
  echo "updated coverage badge: ${PCT}% (${COLOR})"
else
  echo "no <!--COVERAGE--> marker in README.md; coverage is ${PCT}%"
fi

echo "COVERAGE=${PCT}"
