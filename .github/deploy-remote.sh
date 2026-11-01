#!/usr/bin/env bash
# aichat 服务器侧部署逻辑（2026-11-02）。
#
# 这个文件不安装到服务器上，也不常驻。CI 每次把它当剧本，通过 stdin 送进服务器：
#
#   ssh ai@host 'cat > /tmp/aichat.incoming' < aichat-linux      # 上一条：传二进制
#   ssh ai@host 'sudo -n bash -s'           < deploy-remote.sh   # 本脚本：装它
#
# 好处是服务器上没有需要维护的第二份代码，仓库 HEAD 就是部署逻辑的唯一真相。
# 代价是这把 CI 私钥等效于 SSH_USER 的登录权限（见 _local/deploy/cicd-setup.md）。
#
# 自检不通过会自动装回上一个二进制，并把现场日志带到 stderr（Actions 里可见）。
set -euo pipefail

APP_DIR=/opt/aichat
BIN=$APP_DIR/aichat
INCOMING=/tmp/aichat.incoming
SERVICE=aichat
HEALTH_URL=http://127.0.0.1:23015/metrics
HEALTH_TIMEOUT=30
KEEP_BACKUPS=5
MIN_BYTES=5000000

# ssh 的非交互命令不继承登录 shell 的 PATH，必须显式给。
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

log() { printf '[deploy] %s\n' "$*" >&2; }
die() { log "错误：$*"; exit 1; }
cleanup() { rm -f "$INCOMING"; }
trap cleanup EXIT

# 0. 必须 root：后面要动 /opt 和 systemctl。
[ "$(id -u)" -eq 0 ] || die "需要 root 运行，远程命令应为 sudo -n bash -s"

# 1. 校验收到的二进制。挡的是"管道没接上"和"传到一半断了"，
#    不是为了防恶意载荷（能推这个入口的前提是已经拿到了仓库写权限）。
[ -f "$INCOMING" ] || die "没找到 $INCOMING，上传那一步可能失败了"
size=$(stat -c %s "$INCOMING" 2>/dev/null || echo 0)
[ "$size" -ge "$MIN_BYTES" ] || die "收到 $size 字节，小于阈值 $MIN_BYTES，拒绝部署"
magic=$(od -An -tx1 -N4 "$INCOMING" | tr -d ' \n')
[ "$magic" = "7f454c46" ] || die "不是 ELF 文件（magic=$magic），拒绝部署"
chmod 755 "$INCOMING"
log "收到二进制 $size 字节"

# 2. 备份当前二进制。回滚只回二进制，不动数据库——schema 是单向前进的，
#    把旧二进制配新库比"部署失败"更难查，DB 出问题走 data/backup/ 人工处理。
ts=$(date +%Y%m%d-%H%M%S)
backup="$APP_DIR/aichat.bak-$ts"
cp -a "$BIN" "$backup" || die "备份当前二进制失败"
log "已备份 $backup"

# 3. 安装并重启。
install -m 755 -o root -g root "$INCOMING" "$BIN" || die "安装失败"
rm -f "$INCOMING"
systemctl restart "$SERVICE" || die "重启失败"

# 4. 自检：进程活着 + 端口真的在服务（/metrics 200）。
#    只看 is-active 不够——配置写错时进程也可能短暂挂着。
healthy=0
for _ in $(seq 1 "$HEALTH_TIMEOUT"); do
	sleep 1
	if systemctl is-active --quiet "$SERVICE" && curl -fsS -o /dev/null "$HEALTH_URL"; then
		healthy=1
		break
	fi
done

if [ "$healthy" -ne 1 ]; then
	log "自检 ${HEALTH_TIMEOUT}s 未通过，回滚到 $backup"
	install -m 755 -o root -g root "$backup" "$BIN"
	systemctl restart "$SERVICE"
	journalctl -u "$SERVICE" --since "-2min" --no-pager 2>/dev/null | tail -20 >&2 || true
	exit 1
fi

# 5. 结果摘要。版本号来自启动时打的"版本已加载"日志；人设加载也打 version
#    （那是人设版本号），所以必须先按 msg 过滤，否则抓到的永远是最后一条。
since="-2min"
ver=$(journalctl -u "$SERVICE" --since "$since" --no-pager 2>/dev/null \
	| grep -a '版本已加载' | grep -a -o '"version":"[^"]*"' | tail -1 | cut -d'"' -f4 || true)
errs=$(journalctl -u "$SERVICE" --since "$since" --no-pager 2>/dev/null | grep -a -c '"level":"ERROR"' || true)
log "部署成功 version=${ver:-未知} errors=${errs:-0}"

# 6. 只留最近 KEEP_BACKUPS 份备份（这台机器盘不大，二进制 20M 一份）。
ls -1t "$APP_DIR"/aichat.bak-* 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) | while read -r old; do
	log "清理旧备份 $old"
	rm -f "$old"
done
