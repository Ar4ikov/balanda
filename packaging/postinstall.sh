#!/bin/sh
# Ставит профиль AppArmor, если система ограничивает пространства имён
# пользователя (Ubuntu 23.10+). На других системах ничего не делает.
set -e
if [ -f /proc/sys/kernel/apparmor_restrict_unprivileged_userns ] && [ -d /etc/apparmor.d ]; then
	cp /usr/share/balanda/apparmor-balanda /etc/apparmor.d/balanda
	if command -v apparmor_parser >/dev/null 2>&1; then
		apparmor_parser -r /etc/apparmor.d/balanda || true
	fi
fi
exit 0
