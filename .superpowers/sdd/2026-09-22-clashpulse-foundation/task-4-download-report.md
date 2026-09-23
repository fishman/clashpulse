# Task 4 download report

## Status
Done.

## Commit
`feat(download): add bounded fetch client`

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

## Concerns
- None in this slice.
