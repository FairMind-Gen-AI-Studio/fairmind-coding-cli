// Command fairmind is the FairMind Copilot Bridge CLI: a thin protocol adapter
// between shell-capable AI agents (GitHub Copilot CLI / Agents) and the
// FairMind Agent API. It contains no FairMind business logic.
package main

import (
	"os"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/commands"
)

func main() {
	os.Exit(commands.NewApp().Run(os.Args[1:]))
}
