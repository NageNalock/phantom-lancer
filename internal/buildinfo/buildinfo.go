package buildinfo

import "runtime"

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func Current() Info {
	return Info{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
}
