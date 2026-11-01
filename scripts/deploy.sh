#!/usr/bin/env bash
# aichat 服务器侧部署脚本（2026-11-02）。
#
# 由 GitHub Actions 通过专用 SSH 密钥触发：authorized_keys 里这条公钥被
# command="/opt/aichat/deploy.sh" 锁死，转发/pty 全关，所以这个入口只能做一件事
# ——把 stdin 里的二进制装上并自检。密钥泄露的上限就是"用仓库代码部署一次"，
# 拿不到 shell（scp/sftp 也因此不可用，二进制走 stdin）。
#
# 用法（CI 侧）：ssh -i deploy_key ai@host 'ignored' < aichat
# 探活（不部署）：ssh -i deploy_key ai@host 'ping'  → 会因"二进制太小"退出 1，
#   看到这句话就说明连通且锁生效，且没有误装任何东西。
#
# 自检不通过会自动装回上一个二进制，并把原因写到 stderr（Actions 里可见）。
set -euo pipefail

APP_DIR=/opt/aichat
BIN=$APP_DIR/aichat
INCOMING=/tmp/aichat.incoming.$$
SERVICE=aichat
HEALTH_URL=http://127.0.0.1:23015/metrics
HEALTH_TIMEOUT=30
KEEP_BACKUPS=5
MIN_BYTES=5000000

# ssh 的 command 通道不继承登录 shell 的 PATH，必须显式给。
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

log() { printf '[deploy] %s\n' "$*" >&2; }
die() { log "$*"; rm -f "$INCOMING"; exit 1; }

# 1. 收二进制并做最基本的真假判断：大小和 ELF magic。
#    这两条挡的是"管道没接上"和"传到一半断了"，不是为了防恶意载荷
#    （能推这个入口的前提是已经拿到了仓库写权限）。
cat > "$INCOMING"
size=$(stat -c %s "$INCOMING" 2>/dev/null || echo 0)
if [ "$size" -lt "$MIN_BYTES" ]; then
	die "收到 $size 字节，不像一个 aichat 二进制（阈值 $MIN_BYTES）；若只是探活，这里报错即为正常"
fi
magic=$(od -An -tx1 -N4 "$INCOMING" | tr -d ' \n')
if [ "$magic" != "7f454c46" ]; then
	die "不是 ELF 文件（magic=$magic），拒绝部署"
fi
chmod 755 "$INCOMING"

# 2. 备份当前二进制。回滚只回二进制，不动数据库——schema 是单向前进的，
#    把旧二进制配新库比"部署失败"更难查，DB 出问题走 data/backup/ 人工处理。
ts=$(date +%Y%m%d-%H%M%S)
backup="$APP_DIR/aichat.bak-$ts"
sudo cp -a "$BIN" "$backup" || die "备份当前二进制失败"
log "已备份 $backup"

# 3. 安装并重启。
sudo install -m 755 -o root -g root "$INCOMING" "$BIN" || die "安装失败"
rm -f "$INCOMING"
sudo systemctl restart "$SERVICE" || die "重启失败"

# 4. 自检：进程活着 + 端口真的在服务（/metrics 200）。
#    只看 is-active 不够——配置写错时进程也可能短暂挂着。
healthy=0
for _ in $(seq 1 "$HEALTH_TIMEOUT"); do
	sleep 1
	if sudo systemctl is-active --quiet "$SERVICE" && curl -fsS -o /dev/null "$HEALTH_URL"; then
		healthy=1
		break
	fi
done

if [ "$healthy" -ne 1 ]; then
	log "自检 ${HEALTH_TIMEOUT}s 未通过，回滚到 $backup"
	sudo install -m 755 -o root -g root "$backup" "$BIN"
	sudo systemctl restart "$SERVICE"
	# 把现场日志带出去，否则 Actions 里只看到一句"自检失败"
	sudo journalctl -u "$SERVICE" --since "-2min" --no-pager | tail -20 >&2 || true
	exit 1
fi

# 5. 结果摘要：版本号来自程序启动时打的 "版本已加载"，ERROR 计数只作信息。
#    注意字段名会撞车——人设加载也打 version（那是人设版本号），所以必须先按
#    msg 过滤，否则抓到的永远是最后一条"人设已加载"的 version。
since="-2min"
ver=$(sudo journalctl -u "$SERVICE" --since "$since" --no-pager 2>/dev/null \
	| grep '版本已加载' | grep -o '"version":"[^"]*"' | tail -1 | cut -d'"' -f4 || true)
errs=$(sudo journalctl -u "$SERVICE" --since "$since" --no-pager 2>/dev/null | grep -c '"level":"ERROR"' || true)
log "部署成功 version=${ver:-未知} errors=${errs:-0}"

# 6. 只留最近 KEEP_BACKUPS 份备份（这台机器盘不大，二进制 20M 一份）。
ls -1t "$APP_DIR"/aichat.bak-* 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) | while read -r old; do
	log "清理旧备份 $old"
	sudo rm -f "$old"
done
