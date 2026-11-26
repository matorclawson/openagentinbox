module github.com/rodriveracom/openagentinbox

go 1.27.0

require (
	ephemeral v0.3.0
	github.com/google/uuid v1.6.0
	github.com/rodriveracom/dostoevsky v0.3.1-0.20261126003528-94d7dfe927e0
	golang.org/x/crypto v0.50.0
	modernc.org/sqlite v1.57.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.47.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace ephemeral v0.3.0 => ./third_party/ephemeral
