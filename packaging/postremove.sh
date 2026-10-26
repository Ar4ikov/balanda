#!/bin/sh
# Убирает профиль AppArmor, который поставил postinstall.
if [ -f /etc/apparmor.d/balanda ]; then
	if command -v apparmor_parser >/dev/null 2>&1; then
		apparmor_parser -R /etc/apparmor.d/balanda 2>/dev/null || true
	fi
	rm -f /etc/apparmor.d/balanda
fi
exit 0
