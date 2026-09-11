#!/usr/bin/env bash
# Plan and apply the prod environment of the SQLMesh project, then report the run time
# as the metric sqlmesh_run_seconds.
set -euo pipefail
cd sqlmesh
start=${EPOCHREALTIME:-$SECONDS}
# The postgres extra of sqlmesh needs psycopg2 from source. psycopg2-binary gives the same
# module without a compiler.
uv run --python 3.12 --with 'sqlmesh==0.236.2' --with 'psycopg2-binary==2.9.13' sqlmesh plan --auto-apply --no-prompts
end=${EPOCHREALTIME:-$SECONDS}
seconds=$(awk -v s="${start/,/.}" -v e="${end/,/.}" 'BEGIN { printf "%.3f", e - s }')
echo "sqlmesh ran in ${seconds} s"
printf '{"type":"metric","name":"sqlmesh_run_seconds","value":%s,"unit":"s"}\n' "$seconds" >> "$SLUICE_OUTPUTS"
