# ELT example

This example loads data from Postgres with dlt and transforms it with SQLMesh. The flow `elt` in `namespace/` has two tasks:

- `extract` loads every table of the Postgres schema `source` into the schema `raw`. It emits the metric `rows_loaded` for each table and the output `rows`.
- `transform` runs the SQLMesh project in `namespace/sqlmesh`. The project uses a Postgres gateway and keeps its state in Postgres. The model `analytics.customer_orders` gives one row for each customer. The task emits the metric `sqlmesh_run_seconds`.

The tasks install their Python packages with `uv` at run time. The image `sluice-uv` has `uv` and Python 3.12. The first run downloads the packages, so it takes longer.

## Run the demo

`compose.yml` starts Sluice and a demo warehouse. `seed.sql` fills the schema `source` with 3 customers and 5 orders. `setup.py` loads the example into Sluice and runs it.

1. Build the images: `just build-images`.
2. Start the stack from the repository root:

   ```sh
   export SLUICE_BOOTSTRAP_ADMIN_PASSWORD=change-me-now-1
   export SLUICE_MASTER_KEYS="k1:$(openssl rand -base64 32)"
   docker compose -f examples/elt/compose.yml up -d
   ```

3. Load and run the example: `python3 examples/elt/setup.py`.
4. Open <http://localhost:8080>. The execution shows the logs of dlt and SQLMesh. The metric `rows_loaded` shows 3 customers and 5 orders.
5. See the result in the warehouse:

   ```sh
   docker compose -f examples/elt/compose.yml exec warehouse psql -U elt -d warehouse -c "SELECT * FROM analytics.customer_orders"
   ```

## Configure your own database

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

## Faster runs

`uv` installs the packages when a task starts. The time that this takes depends on the executor:

- Process executor: `uv` keeps its cache in the Sluice container. Only the first run downloads the packages.
- Docker and kubernetes executors: each task starts in a new container with an empty cache, so each run downloads the packages again.

For docker and kubernetes, build an image that already has the packages, and use it as the executor image:

```dockerfile
FROM sluice-uv:<version>
RUN uv pip install --system --python 3.12 "dlt[postgres,sql_database]==1.30.0" "psycopg2-binary==2.9.13" "sqlalchemy==2.0.52" "sqlmesh==0.236.2"
```

Then set it in `namespace.yaml`: `defaults: { executor: { type: kubernetes, image: <your-image> } }`. `uv` finds the installed packages and does not download them.

## Executors

The flow runs on the process executor of the `sluice-uv` image. To run it on the kubernetes executor, add `defaults: { executor: { type: kubernetes, image: sluice-uv:<version> } }` to `namespace.yaml`.
