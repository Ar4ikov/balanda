#!/bin/sh
# Генерирует файлы автодополнения для архивов и пакетов.
set -eu
rm -rf completions
mkdir completions
for sh in bash zsh fish; do
	go run . completion "$sh" >"completions/balanda.$sh"
done
go run . completion powershell >completions/balanda.ps1
