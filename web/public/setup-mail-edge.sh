#!/usr/bin/env bash
# Dedicated Debian/Ubuntu VPS only. Never adopt an unmanaged Postfix configuration.
set -euo pipefail
[[ $# == 3 && $EUID == 0 ]] || { echo "Usage: sudo bash setup-mail-edge.sh DOMAIN MX-HOSTNAME DKIM-SELECTOR" >&2; exit 2; }
domain=$1; mx=$2; selector=$3
dns='^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'
[[ $domain =~ $dns && $mx =~ $dns && $selector =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || exit 2
source /etc/os-release
[[ $ID == debian || $ID == ubuntu ]] || { echo "Use the existing mail server guide on this distribution."; exit 2; }
id openagentinbox >/dev/null
systemctl is-active --quiet openagentinbox.service
state=/etc/openagentinbox/managed-edge
exec 9>/run/openagentinbox-edge-install.lock
flock -n 9 || { echo "Another setup is running."; exit 1; }
if [[ ! -e $state/hostname ]]; then
 [[ ! -e /etc/postfix/main.cf && ! -e /etc/opendkim.conf && ! -e /usr/sbin/sendmail ]] || { echo "An existing mail server was found. Follow email-setup.md to preserve its settings."; exit 1; }
 install -d -m 0700 "$state"
 printf '%s\n' "$mx" >"$state/hostname"
fi
[[ $(cat "$state/hostname") == "$mx" ]] || { echo "Use the existing edge hostname for additional domains."; exit 1; }
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y postfix opendkim opendkim-tools
postconf -m | grep -Fx socketmap >/dev/null || { echo "Postfix needs socketmap support."; exit 1; }
install -d -o openagentinbox -g postfix -m 2750 /var/spool/postfix/oai
install -d /etc/systemd/system/openagentinbox.service.d
cat >/etc/systemd/system/openagentinbox.service.d/mail-edge.conf <<'UNIT'
[Service]
ReadWritePaths=/var/spool/postfix/oai
Environment=INBOX_EMAIL_SOCKET=/var/spool/postfix/oai/lmtp
Environment=INBOX_EMAIL_RELAY=127.0.0.1:25
UNIT
keydir=/etc/opendkim/keys/$domain
install -d -m 0750 -o opendkim -g opendkim "$keydir"
if [[ ! -e "$keydir/$selector.private" ]]; then
 (umask 077; opendkim-genkey -D "$keydir" -d "$domain" -s "$selector" -b 2048)
fi
chown opendkim:opendkim "$keydir/$selector.private"
chmod 0600 "$keydir/$selector.private"
install -d /etc/opendkim
touch /etc/opendkim/KeyTable /etc/opendkim/SigningTable
key="$selector._domainkey.$domain"
grep -Fqx "$key $domain:$selector:$keydir/$selector.private" /etc/opendkim/KeyTable || printf '%s %s:%s:%s\n' "$key" "$domain" "$selector" "$keydir/$selector.private" >>/etc/opendkim/KeyTable
# Replace just this managed domain's mapping on a selector change.
awk -v addr="*@$domain" '$1!=addr' /etc/opendkim/SigningTable >"$state/signing.next"
printf '*@%s %s\n' "$domain" "$key" >>"$state/signing.next"
install -m 0644 "$state/signing.next" /etc/opendkim/SigningTable
cat >/etc/opendkim.conf <<'DKIM'
Syslog yes
Mode sv
UserID opendkim
Socket inet:8891@127.0.0.1
Canonicalization relaxed/simple
KeyTable refile:/etc/opendkim/KeyTable
SigningTable refile:/etc/opendkim/SigningTable
DKIM
postconf -e "myhostname = $mx" 'mydestination = localhost' 'inet_interfaces = all' 'mynetworks = 127.0.0.0/8 [::1]/128' \
 'smtpd_relay_restrictions = permit_mynetworks, reject_unauth_destination' 'smtpd_reject_unlisted_recipient = yes' \
 'virtual_mailbox_domains = proxy:socketmap:unix:/var/spool/postfix/oai/lmtp.map:domains' \
 'virtual_mailbox_maps = proxy:socketmap:unix:/var/spool/postfix/oai/lmtp.map:inboxes' \
 'virtual_transport = lmtp:unix:oai/lmtp' 'lmtp_destination_recipient_limit = 1' 'message_size_limit = 1000000' \
 'smtpd_milters = inet:127.0.0.1:8891' 'milter_default_action = tempfail' 'smtp_tls_security_level = may' 'smtpd_tls_security_level = may'
postfix check
systemctl daemon-reload
systemctl restart openagentinbox.service opendkim.service postfix.service
echo "Copy the quoted DKIM fragments below as one TXT value, without quotes or parentheses. Only the public key is printed."
install -d -m 0755 /etc/openagentinbox/dkim-public
awk -F'"' '{for(i=2;i<=NF;i+=2) printf "%s",$i} END{print ""}' "$keydir/$selector.txt" >"$state/public.next"
install -m 0644 "$state/public.next" "/etc/openagentinbox/dkim-public/$domain.$selector.txt"
cat "$state/public.next"
echo "Return to Mail setup and click Refresh connection. Configure a public TLS certificate for the MX hostname, check firewall port 25 and set reverse DNS at the VPS provider."
