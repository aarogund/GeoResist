# GeoResist processing workflow changes

## Measurement ingestion
- Survey metadata and measurements are validated before any database writes.
- CSV, `.xlsx`, and manual entry now share one validation and persistence path.
- Legacy `.xls` is explicitly rejected because the configured Excel library reads `.xlsx`; convert it to `.xlsx` or CSV.
- Each non-empty row must contain positive AB/2 and either positive apparent resistivity, or resistance that can be combined with a geometric factor.
- Voltage/current inputs can produce resistance using R = V/I when both are positive. Enter voltage in volts and current in amperes.
- Invalid numeric values, non-finite values, invalid Schlumberger spacing, empty rows with partial data, and unsupported files are rejected with row-specific errors.
- Measurement insertion and survey insertion use one PostgreSQL transaction, so failures roll back both.
- The database insert persists the measurement columns already defined in the schema, including serial number, voltage, current, geometric factor, resistance, and apparent resistivity.
- Uploads are limited to 20 MB for measurement files and requests are limited to 25 MB.

## Schlumberger geometric factor
`CalculateK` now uses the standard Schlumberger expression for half-spacings AB/2 and MN/2:

K = pi * ((AB/2)^2 - (MN/2)^2) / (2 * (MN/2))

This assumes AB/2 and MN/2 are in the same distance unit (normally metres) and resistance is in ohms, producing apparent resistivity in ohm-metres. Verify that the field instrument's geometry and units match these assumptions.

## Migration safety
`database.Migrate()` no longer drops existing tables. The previous drop statements would erase users, surveys, measurements, layers, and reports whenever `RUN_MIGRATION=true`. This change prevents that destructive behavior. The current migration uses `CREATE TABLE IF NOT EXISTS`; it does not automatically alter an already-existing table to add missing columns, so any future schema changes should be implemented as explicit, versioned `ALTER TABLE` migrations.

## Validation status
The code was formatted with `gofmt`. A full `go test ./...` could not be completed in this environment because the module declares Go 1.25.0 and the Go toolchain/dependencies were not available for download. Run `go test ./...` in an environment with Go 1.25 and network access or with the required dependencies cached before deployment.

## Implemented processing guard and recommended verification order
- The results handler now skips the saved-model inversion when the stored layer count is less than two. Previously, the invalid count was logged but execution continued into the measurement threshold and layer-detection path.
- The results pipeline uses the validated, sorted, averaged measurements for both the observed curve and the fitted curve, and only sends a computed curve to Plotly when its length matches the observed data and every computed value is finite and positive.

Verify the full workflow in this order:
1. **Forward-model unit tests:** retain the reference-spacing tests for each supported layer model and reject non-positive/non-finite inputs.
2. **Measurement persistence:** test manual entry, CSV, and XLSX ingestion; verify geometry/resistivity calculations and transaction rollback on invalid rows.
3. **Results-handler data flow:** verify ownership/authentication, DB scan order, validation, sorting, duplicate-AB/2 averaging, and that observed and computed arrays use the same averaged AB/2 spacings.
4. **Inversion validation:** test two- and three-layer comparisons against synthetic data, parameter counts, finite outputs, fit metrics, and underdetermined/invalid layer counts.
5. **Plot and report path:** verify no fitted line is rendered when fitting fails; label model parameters as estimates and preserve uncertainty/limitations rather than asserting unsupported geological conclusions.
6. **End-to-end database tests:** exercise create survey -> ingest measurements -> results -> edit/report, including rollback and cross-user access denial.

The current runtime in this environment could not download the Go 1.25 toolchain, so these changes have been formatted but not compiled or test-run here. Run `go test ./...` with Go 1.25 and dependencies available before deployment.

## Results, inversion, and report workflow additions
- The results page now displays the observed curve classification, model status, layer parameters, and fit-quality metrics when a valid forward response is available.
- A saved user-edited layer model takes precedence for the displayed computed curve; viewing results does not write to the database.
- The result editor reads and saves the existing `layers` table instead of querying a nonexistent `interpretations` table. Saving is transaction-based and checks survey ownership before changing layer rows.
- The “Save and Recalculate Fit” action now validates the proposed model with the forward model and fit-metric calculation before persisting it. The bottom layer is stored as a half-space with a nullable thickness.
- The new-report form now starts with an editable draft generated from survey metadata, measurement counts/range, and any saved layer model. It explicitly avoids inferring aquifer presence or a drill depth from resistivity alone.
- Report PDF, report sharing POST actions, and public expiring share-token downloads are wired into the router. Share-token revocation now checks survey ownership.
- Nullable database values for survey metadata and measurement columns are normalized before scanning into non-nullable Go fields. The transition-zone form option now matches the server's accepted value.
- The results and report views retain the logarithmic apparent-resistivity axis with the configured tick values 100, 200, 500, 1000, 2000, 5000, and 10000 Ω·m.

## Validation note
- `gofmt` completed for the changed Go files.
- All Go files passed Go parser syntax checks, and all HTML templates parsed successfully with `html/template`.
- Full `go test ./...` could not run in this environment: the project requests Go 1.25 and the environment only has Go 1.23.2; dependencies/toolchain downloads are blocked by network access. Run the full test suite in the project's normal Go 1.25 environment before deploying.
