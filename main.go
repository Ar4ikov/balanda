// balanda — песочница для команд, уборка мусора, генератор воды и байки
// в терминале.
package main

import (
	"os"

	"github.com/Ar4ikov/balanda/internal/cli"
	"github.com/Ar4ikov/balanda/internal/kamera"
)

func main() {
	// Служебные режимы песочницы (надзиратель и запуск под rlimit)
	// разбираются до cobra: им не нужны ни справка, ни флаги.
	if code, ok := kamera.Dispatch(os.Args); ok {
		os.Exit(code)
	}
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
