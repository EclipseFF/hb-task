package workflow

import "embed"

//go:embed *.bpmn *.dmn *.form
var Files embed.FS
