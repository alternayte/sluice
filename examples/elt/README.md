# ELT example

This example loads data from Postgres with dlt and transforms it with SQLMesh. The flow `elt` in `namespace/` has two tasks:

- `extract` loads every table of the Postgres schema `source` into the schema `raw`. It emits the metric `rows_loaded` for each table and the output `rows`.
- `transform` runs the SQLMesh project in `namespace/sqlmesh`. The project uses a Postgres gateway and keeps its state in Postgres. The model `analytics.customer_orders` gives one row for each customer. The task emits the metric `sqlmesh_run_seconds`.

The tasks install their Python packages with `uv` at run time. The image `sluice-uv` has `uv` and Python 3.12. The first run downloads the packages, so it takes longer.

## Configure

The flow reads the connection from namespace variables and one secret. Set them in the namespace `elt`:

| Name | Kind | Example |
|---|---|---|
| `PG_HOST` | variable | `postgres` |
| `PG_PORT` | variable | `5432` |
| `PG_DATABASE` | variable | `warehouse` |
| `PG_USER` | variable | `elt` |
| `ELT_PG_PASSWORD` | secret | the password of `PG_USER` |

The user needs these rights: read the schema `source`, and create the schemas `raw`, `analytics`, `sqlmesh` and `sqlmesh__analytics`.

## Run

1. Validate the files: `sluice validate examples/elt/namespace`.
2. Create the namespace `elt` and upload the files of `namespace/`.
3. Set the variables and the secret.
4. Run the flow `elt`.

The flow runs on the process executor of the `sluice-uv` image. To run it on the kubernetes executor, add `defaults: { executor: { type: kubernetes, image: sluice-uv:<version> } }` to `namespace.yaml`.
