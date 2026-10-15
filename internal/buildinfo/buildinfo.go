// Package buildinfo 提供构建信息，由构建脚本通过
// -ldflags "-X oci-help/internal/buildinfo.Version=..." 注入。
package buildinfo

// 构建信息。直接 go build 时为下列默认值。
var (
	Version   = "dev"
	Commit    = ""
	BuildTime = ""
)
