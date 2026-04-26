module github.com/branchkit/plugins/keyboard

go 1.24

require (
	github.com/a-h/templ v0.3.1001
	github.com/branchkit/plugin-sdk-go v0.0.0
	github.com/branchkit/plugin-toolkit-go v0.0.0
)

replace (
	github.com/branchkit/plugin-sdk-go => ../../../plugin-sdk-go
	github.com/branchkit/plugin-toolkit-go => ../../../plugin-toolkit-go
)
