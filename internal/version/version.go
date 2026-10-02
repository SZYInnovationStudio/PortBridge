package version

var (
	// Version 当前版本号，编译时可通过 -ldflags 覆盖
	Version = "0.1.0"
	// BuildTime 构建时间，编译时注入
	BuildTime = "dev"
)
