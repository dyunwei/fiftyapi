# Binary release quickstart

[简体中文](../release-quickstart.md) | English

The release package contains `fiftyapi` for the build machine's operating system and architecture, with the interface embedded. For a new installation, run from the package directory:

For a new installation, you can simply run `./fiftyapi serve` with both key variables unset. The service saves `master.key` and `setup-token` beside the database (under `data/` by default). Read `data/setup-token` for administrator setup and back up `data/master.key`. Existing databases require their original key. The following is an optional manual configuration recipe:

```sh
umask 077
mkdir -p secrets data
# Generate only once. An existing database requires its ORIGINAL master key.
openssl rand -base64 32 > secrets/master.key
openssl rand -hex 32 > secrets/setup-token
export FIFTYAPI_MASTER_KEY="$(cat secrets/master.key)"
export FIFTYAPI_SETUP_TOKEN="$(cat secrets/setup-token)"
export FIFTYAPI_DB_PATH=data/fiftyapi.db
export FIFTYAPI_LISTEN=127.0.0.1:8080
./fiftyapi serve
```

Open http://127.0.0.1:8080. Read `secrets/setup-token` locally and enter it during administrator setup. Choose **English** in the top-right corner if necessary. On subsequent starts, export the same values and run the service; **do not regenerate or overwrite the master key**.

In a release package, see `docs/en/deployment.md`, `docs/en/clients.md` and `docs/en/compatibility.md`. The root README describes a source checkout; its build commands require source files.

Use `SHA256SUMS` to verify the binary. Preserve LICENSE, COPYING.GPL-3, THIRD_PARTY_NOTICES.md and licenses/. Redistribution must include access to the matching complete source and build materials.
