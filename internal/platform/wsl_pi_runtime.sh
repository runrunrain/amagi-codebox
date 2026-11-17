# CodeBox WSL Pi: keep configuration/auth/plugins in the canonical shared
# agent root, but put high-frequency task state and artifact writes on
# the distro filesystem. Existing explicit overrides always win.
_cb_pi_state="${XDG_STATE_HOME:-$HOME/.local/state}/amagi-codebox/pi"
case "$_cb_pi_state" in
  /mnt/*|[A-Za-z]:*|[!/]*) _cb_pi_state="$HOME/.local/state/amagi-codebox/pi" ;;
esac
case "$_cb_pi_state" in
  /*) ;;
  *) _cb_pi_state='' ;;
esac
_cb_pi_old_umask=$(umask)
umask 077
if [ -n "$_cb_pi_state" ] && mkdir -p "$_cb_pi_state"; then
  _cb_pi_fs=$(stat -f -c %T "$_cb_pi_state" 2>/dev/null || true)
  case "$_cb_pi_fs" in
    9p|drvfs|fuse*|cifs|smb*|nfs*|ntfs*|'' )
      printf '%s\n' 'CodeBox: WSL Pi 运行目录仍在跨系统文件系统，未启用本地 I/O 优化；请将 WSL 用户主目录放在本地 Linux 文件系统。' >&2
      ;;
    *)
      if mkdir -p "$_cb_pi_state/data" "$_cb_pi_state/artifacts"; then
        : "${AMAGI_DATA_DIR:=$_cb_pi_state/data}"
        : "${AMAGI_ARTIFACT_ROOT:=$_cb_pi_state/artifacts}"
        export AMAGI_DATA_DIR AMAGI_ARTIFACT_ROOT
      else
        printf '%s\n' 'CodeBox: 无法创建 WSL Pi 本地运行目录，沿用现有运行数据位置。' >&2
      fi
      ;;
  esac
else
  printf '%s\n' 'CodeBox: 无法创建 WSL Pi 本地运行目录，沿用现有运行数据位置。' >&2
fi
umask "$_cb_pi_old_umask"
unset _cb_pi_state _cb_pi_fs _cb_pi_old_umask
