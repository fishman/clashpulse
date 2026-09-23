# Task 4 download report

## Status
Done.

## Commit
- `feat(download): add bounded fetch client`
- `fix(download): tighten fetch policy`

## Changed files
- `download/client.go`
- `download/fetch_test.go`

## Red / green evidence

### Red
```sh
go test ./download -run 'TestFetch(UsesRouteValidatorsAndBodyLimit|RejectsHTTPAndUserinfo|FollowsRedirectsAndBoundsBody|Returns304Bodyless)$'
```
Failed before implementation: `undefined: Route`, `undefined: NewClient`, `undefined: Request`, `undefined: Direct`, `undefined: MihomoProxy`.

### Green
```sh
go test ./download
```
```text
ok  github.com/fishman/clashpulse/download
```

## Fix round 1

### Red
```sh
go test ./download -run 'TestFetch(UsesRouteValidatorsAndBodyLimit|RejectsHTTPAndUserinfo|RejectsDisallowedRedirectTargets|ConditionalHeadersStayOnOriginalAndSameOriginOnly|Returns304ValidatorsAndBodyless|CancelsDuringRead|FailsOnSixthRedirect)$'
```
Failed with the expected regressions: cross-origin validators leaked, 304 omitted validators, cancellation was ignored, and the sixth redirect was accepted.

### Green
```sh
go test ./download
```
```text
ok  github.com/fishman/clashpulse/download
```

## Concerns
- None in this slice.
