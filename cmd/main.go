// Copyright (c) 2015-2021 MinIO, Inc.
//
// This file is part of MinIO Object Storage stack
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/minio/cli"
	"github.com/minio/minio/internal/color"
	"github.com/minio/minio/internal/logger"
	"github.com/minio/pkg/v3/console"
	"github.com/minio/pkg/v3/env"
	"github.com/minio/pkg/v3/trie"
	"github.com/minio/pkg/v3/words"
)

// GlobalFlags - global flags for minio.
var GlobalFlags = []cli.Flag{
	// Deprecated flag, so its hidden now - existing deployments will keep working.
	cli.StringFlag{
		Name:   "config-dir, C",
		Value:  defaultConfigDir.Get(),
		Usage:  "[DEPRECATED] path to legacy configuration directory",
		Hidden: true,
	},
	cli.StringFlag{
		Name:  "certs-dir, S",
		Value: defaultCertsDir.Get(),
		Usage: "path to certs directory",
	},
	cli.BoolFlag{
		Name:  "quiet",
		Usage: "disable startup and info messages",
	},
	cli.BoolFlag{
		Name:  "anonymous",
		Usage: "hide sensitive information from logging",
	},
	cli.BoolFlag{
		Name:  "json",
		Usage: "output logs in JSON format",
	},
	// Deprecated flag, so its hidden now, existing deployments will keep working.
	cli.BoolFlag{
		Name:   "compat",
		Usage:  "enable strict S3 compatibility by turning off certain performance optimizations",
		Hidden: true,
	},
	// This flag is hidden and to be used only during certain performance testing.
	cli.BoolFlag{
		Name:   "no-compat",
		Usage:  "disable strict S3 compatibility by turning on certain performance optimizations",
		Hidden: true,
	},
}

// Help template for minio.
var minioHelpTemplate = `NAME:
  {{.Name}} - {{.Usage}}

DESCRIPTION:
  {{.Description}}

USAGE:
  {{.HelpName}} {{if .VisibleFlags}}[FLAGS] {{end}}COMMAND{{if .VisibleFlags}}{{end}} [ARGS...]

COMMANDS:
  {{range .VisibleCommands}}{{join .Names ", "}}{{ "\t" }}{{.Usage}}
  {{end}}{{if .VisibleFlags}}
FLAGS:
  {{range .VisibleFlags}}{{.}}
  {{end}}{{end}}
VERSION:
  {{.Version}}
`

func newApp(name string) *cli.App {
	// 创建一个空的命令集合，用于存储当前支持的 MinIO 命令。
	commands := []cli.Command{}

	// 创建一个 Trie 树，用于存储当前支持的 MinIO 命令。
	commandsTree := trie.NewTrie()

	// 定义一个函数用于注册 CLI 命令。
	registerCommand := func(command cli.Command) {
		// 避免注册未构建的命令（通过 go:build 标签）。
		if command.Name == "" {
			return
		}
		// 将命令添加到命令集合中。
		commands = append(commands, command)
		// 将命令名称插入到 Trie 树中。
		commandsTree.Insert(command.Name)
	}

	// 定义一个函数用于查找最接近的命令。
	findClosestCommands := func(command string) []string {
		var closestCommands []string
		// 通过前缀匹配查找命令。
		closestCommands = append(closestCommands, commandsTree.PrefixMatch(command)...)

		// 对找到的命令进行排序。
		sort.Strings(closestCommands)
		// 建议其他接近的命令，允许错过、错误添加和字符调换。
		for _, value := range commandsTree.Walk(commandsTree.Root()) {
			if sort.SearchStrings(closestCommands, value) < len(closestCommands) {
				continue
			}
			// 2 是一个任意值，表示允许的最大输入错误数。
			if words.DamerauLevenshteinDistance(command, value) < 2 {
				closestCommands = append(closestCommands, value)
			}
		}

		return closestCommands
	}

	// 注册所有命令。
	registerCommand(serverCmd)
	registerCommand(fmtGenCmd)

	// 设置应用程序。
	cli.HelpFlag = cli.BoolFlag{
		Name:  "help, h",
		Usage: "show help",
	}
	cli.VersionPrinter = printMinIOVersion

	// 创建一个新的 CLI 应用程序。
	app := cli.NewApp()
	app.Name = name
	app.Author = "MinIO, Inc."
	app.Version = ReleaseTag
	app.Usage = "High Performance Object Storage"
	app.Description = `Build high performance data infrastructure for machine learning, analytics and application data workloads with MinIO`
	app.Flags = GlobalFlags
	app.HideHelpCommand = true // 隐藏 `help, h` 命令，因为我们已经有 `minio --help`。
	app.Commands = commands
	app.CustomAppHelpTemplate = minioHelpTemplate
	app.CommandNotFound = func(ctx *cli.Context, command string) {
		console.Printf("‘%s’ is not a minio sub-command. See ‘minio --help’.\n", command)
		closestCommands := findClosestCommands(command)
		if len(closestCommands) > 0 {
			console.Println()
			console.Println("Did you mean one of these?")
			for _, cmd := range closestCommands {
				console.Printf("\t‘%s’\n", cmd)
			}
		}

		os.Exit(1)
	}

	return app
}

func startupBanner(banner io.Writer) {
	CopyrightYear = strconv.Itoa(time.Now().Year())
	fmt.Fprintln(banner, color.Blue("Copyright:")+color.Bold(" 2015-%s MinIO, Inc.", CopyrightYear))
	fmt.Fprintln(banner, color.Blue("License:")+color.Bold(" "+MinioLicense))
	fmt.Fprintln(banner, color.Blue("Version:")+color.Bold(" %s (%s %s/%s)", ReleaseTag, runtime.Version(), runtime.GOOS, runtime.GOARCH))
}

func versionBanner(c *cli.Context) io.Reader {
	banner := &strings.Builder{}
	fmt.Fprintln(banner, color.Bold("%s version %s (commit-id=%s)", c.App.Name, c.App.Version, CommitID))
	fmt.Fprintln(banner, color.Blue("Runtime:")+color.Bold(" %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH))
	fmt.Fprintln(banner, color.Blue("License:")+color.Bold(" GNU AGPLv3 - https://www.gnu.org/licenses/agpl-3.0.html"))
	fmt.Fprintln(banner, color.Blue("Copyright:")+color.Bold(" 2015-%s MinIO, Inc.", CopyrightYear))
	return strings.NewReader(banner.String())
}

func printMinIOVersion(c *cli.Context) {
	io.Copy(c.App.Writer, versionBanner(c))
}

var debugNoExit = env.Get("_MINIO_DEBUG_NO_EXIT", "") != ""

// Main main for minio server.
func Main(args []string) {
	// 获取应用程序的名称，通常是可执行文件的名称。
	appName := filepath.Base(args[0])

	// 检查是否设置了调试模式下不退出的环境变量。
	if debugNoExit {
		// 定义一个冻结函数，用于阻止程序退出。
		freeze := func(_ int) {
			// 无限阻塞操作，防止程序退出。
			<-make(chan struct{})
		}

		// 覆盖 logger 的 os.Exit() 函数，使其调用冻结函数。
		logger.ExitFunc = freeze

		// 使用 defer 确保在函数退出前执行以下代码块。
		defer func() {
			// 捕获并处理 panic。
			if err := recover(); err != nil {
				// 打印 panic 信息。
				fmt.Println("panic:", err)
				fmt.Println("")
				// 打印堆栈信息。
				fmt.Println(string(debug.Stack()))
			}
			// 调用冻结函数，阻止程序退出。
			freeze(-1)
		}()
	}

	// 运行应用程序，如果发生错误则退出程序。
	if err := newApp(appName).Run(args); err != nil {
		// 退出程序，返回状态码 1。
		os.Exit(1) //nolint:gocritic
	}
}
