# Admin live smoke (real Go API, temporary SQLite)

`builder.live.spec.ts` drives the real admin UI against the real Go server:
admin auth and CSRF, `/admin/api/builders*` (atomic configuration save,
ownership checks), Preview and `/sub`. Nothing is mocked. The stand
(`internal/web/admin_live_smoke_test.go`, build tag `adminsmoke`) uses a
temporary SQLite database and a local upstream with two test catalogues; it
never contacts a real provider or database.

1. Build the admin bundle that the Go server embeds:

   ```sh
   npm --prefix frontend run build:admin
   ```

2. Start the stand (CGO is required for SQLite; Docker example, LF copy of the
   repository as for the other Go checks):

   ```sh
   docker run --rm -p 127.0.0.1:18181:18181 -v "$PWD:/src:ro" -v "$STATE_DIR:/state" \
     -e CGO_ENABLED=1 -e ADMIN_SMOKE_ADDR=0.0.0.0:18181 -e ADMIN_SMOKE_ORIGIN=https://localhost:18181 \
     -e ADMIN_SMOKE_PASSWORD="$PASSWORD" -e ADMIN_SMOKE_STATE_DIR=/state golang:1.25.14 \
     bash -c 'cp -r /src /w && cd /w && go test -tags adminsmoke -run TestAdminLiveSmokeServer -count=1 -timeout 20m -v ./internal/web/'
   ```

   It writes `state.json` to the state directory when it is ready.

3. Run the spec (it writes the `stop` file at the end, which ends the stand):

   ```sh
   ADMIN_LIVE_STATE_DIR="$STATE_DIR" ADMIN_LIVE_PASSWORD="$PASSWORD" npm --prefix frontend run test:browser:admin-live
   ```

The admin namespace requires an https `SITE_URL`, so the stand serves the
browser through a local TLS proxy with a self-signed test certificate at
`https://localhost:18181` (the live Playwright config ignores that
certificate only).
