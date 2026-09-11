# /// script
# requires-python = ">=3.12"
# dependencies = [
#   "dlt[postgres,sql_database]==1.30.0",
#   "psycopg2-binary==2.9.13",
#   "sqlalchemy==2.0.52",
# ]
# ///
"""Load every table of the Postgres schema `source` into the schema `raw` with dlt.

The task emits the metric `rows_loaded` for each table and the output `rows`.
"""

import json
import os
from urllib.parse import quote

import dlt
from dlt.sources.sql_database import sql_database


def pg_url() -> str:
    """Return the connection URL from the task environment."""
    return "postgresql://{user}:{password}@{host}:{port}/{database}".format(
        user=quote(os.environ["PG_USER"], safe=""),
        password=quote(os.environ["PG_PASSWORD"], safe=""),
        host=os.environ["PG_HOST"],
        port=os.environ.get("PG_PORT") or "5432",
        database=os.environ["PG_DATABASE"],
    )


def emit(event: dict) -> None:
    """Write one output or metric event for Sluice."""
    with open(os.environ["SLUICE_OUTPUTS"], "a", encoding="utf-8") as f:
        f.write(json.dumps(event) + "\n")


def main() -> None:
    url = pg_url()
    pipeline = dlt.pipeline(
        pipeline_name="elt_extract",
        destination=dlt.destinations.postgres(credentials=url),
        dataset_name="raw",
        # The pipeline state stays in the task work directory.
        pipelines_dir=os.path.join(os.getcwd(), ".dlt-pipelines"),
    )
    info = pipeline.run(sql_database(credentials=url, schema="source"), write_disposition="replace")
    print(info)
    counts = pipeline.last_trace.last_normalize_info.row_counts
    total = 0
    for table, rows in sorted(counts.items()):
        if table.startswith("_dlt"):
            continue
        print(f"loaded {rows} rows into raw.{table}")
        emit({"type": "metric", "name": "rows_loaded", "value": rows, "tags": {"table": table}})
        total += rows
    emit({"type": "output", "key": "rows", "value": total})


if __name__ == "__main__":
    main()
