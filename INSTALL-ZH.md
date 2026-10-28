# Flyboard RC1 详细安装与运维教程

> 状态说明：这是独立 clean-room 实现的发布候选版，不依赖 Xboard/V2Board 源码、镜像或安装命令。核心自动化与真实 PostgreSQL/Valkey 集成测试已通过；仍需在你的全新服务器完成最终黑盒验收后才能标记正式版。

## 1. 推荐服务器配置

- 系统：Ubuntu 22.04/24.04 LTS 或 Debian 12（64 位）
- CPU：最低 2 核，推荐 4 核
- 内存：最低 4 GB，推荐 8 GB；本包首次会在服务器本地编译 5 个 Go 镜像
- 磁盘：最低剩余 20 GB，推荐 40 GB 以上
- 公网：开放 80/443；SSH 端口按你的实际配置开放
- 域名：推荐提前把面板域名 A/AAAA 记录解析到服务器
- 必须安装：Docker Engine、Docker Compose v2、curl、openssl、tar

不要在承载重要生产业务且内存不足的服务器上直接试装。正式部署必须使用 HTTPS。

---

## 2. 校验安装包（只读检查）

把以下两个文件上传到新服务器同一目录：

- `flyboard-rc1.tar.gz`
- `flyboard-rc1.tar.gz.sha256`

进入文件所在目录：

```bash
cd /root
sha256sum -c flyboard-rc1.tar.gz.sha256
```

预期看到：

```text
flyboard-rc1.tar.gz: OK
```

查看压缩包内容（不会修改系统）：

```bash
tar -tzf flyboard-rc1.tar.gz | head -50
```

---

## 3. 安装 Docker（真正执行，会修改系统）

如果服务器已经安装 Docker Engine 和 Compose v2，跳到第 4 节。

### Ubuntu / Debian 官方 Docker 仓库方式

先卸载可能冲突的旧包：

```bash
sudo apt-get remove -y docker.io docker-doc docker-compose podman-docker containerd runc || true
```

安装仓库依赖：

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg openssl tar
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/$(. /etc/os-release && echo "$ID")/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
```

添加 Docker 仓库：

```bash
. /etc/os-release
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$ID $VERSION_CODENAME stable" | sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
sudo apt-get update
```

安装 Docker：

```bash
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker
```

### 验证 Docker（只读检查）

```bash
docker version
docker compose version
sudo systemctl --no-pager --full status docker
```

如果当前用户不是 root，可加入 docker 组（会修改用户组，重新登录后生效）：

```bash
sudo usermod -aG docker "$USER"
```

---

## 4. 解压 Flyboard（真正执行，会创建文件）

建议固定安装到 `/opt/flyboard`：

```bash
sudo mkdir -p /opt/flyboard
sudo tar -xzf /root/flyboard-rc1.tar.gz -C /opt/flyboard
cd /opt/flyboard
sudo chown -R root:root /opt/flyboard
sudo chmod 755 /opt/flyboard
```

确认核心文件存在（只读检查）：

```bash
cd /opt/flyboard
ls -la
ls -la install.sh compose.yaml Dockerfile
```

---

## 5. 安装前检查（只读检查）

检查 CPU、内存、磁盘与端口：

```bash
nproc
free -h
df -h /
sudo ss -lntp | grep -E ':(80|443|7001)\b' || true
docker ps
```

建议：

- 可用内存至少 3 GB；本地首次构建时越多越好。
- 磁盘至少剩余 20 GB。
- Flyboard 快速安装默认监听 `0.0.0.0:7001`，可直接通过服务器 IP 访问；生产环境请用防火墙限制访问并尽快配置 HTTPS。
- 安装脚本采用 `COMPOSE_PARALLEL_LIMIT=1`，避免并行构建压垮小服务器。

检查 Compose 配置（只读验证，不启动服务）：

```bash
cd /opt/flyboard
docker compose config >/tmp/flyboard-compose-check.yml
echo "Compose 配置检查通过"
```

---

## 6. 一键安装 Flyboard（真正执行，会创建容器、卷和密钥）

### 默认端口 7001

```bash
cd /opt/flyboard
sudo ./install.sh | tee /root/flyboard-first-install.txt
```

安装器会优先自动显示服务器地址。若服务器位于 NAT 后、自动显示的不是公网地址，可显式指定：

```bash
sudo env FLYBOARD_PUBLIC_HOST='你的公网IP或域名' ./install.sh | tee /root/flyboard-first-install.txt
```

### 自定义本机监听端口

例如改为 18080：

```bash
cd /opt/flyboard
sudo env FLYBOARD_PORT=18080 ./install.sh | tee /root/flyboard-first-install.txt
```

### 自定义初始管理员邮箱和密码

> 密码会出现在当前命令和 shell 历史中；更推荐让安装器自动生成一次性密码。

```bash
cd /opt/flyboard
sudo env \
  FLYBOARD_ADMIN_EMAIL='admin@example.com' \
  FLYBOARD_ADMIN_PASSWORD='请替换为至少20位强密码' \
  ./install.sh | tee /root/flyboard-first-install.txt
```

安装器会自动：

1. 生成随机 Compose 项目名；
2. 生成 PostgreSQL、Valkey、管理路径和主加密密钥；
3. 将 secrets 权限设为 `0600`；
4. 启动 PostgreSQL 与 Valkey；
5. 执行数据库迁移；
6. 启动 panel、worker、scheduler；
7. 等待健康检查，并确认管理页面、CSS、JavaScript 与安全解析器均可访问；
8. 首次安装时只输出一次管理员邮箱和密码。

请立即保存输出中的：

- `Admin URL`
- `Admin email`
- `One-time password`

再次运行 `./install.sh` 不会重新签发管理员密码。

---

## 7. 安装后验证（只读验证）

### 查看容器

```bash
cd /opt/flyboard
sudo docker compose ps
```

`postgres`、`valkey`、`panel`、`worker`、`scheduler` 应为运行状态，带健康检查的服务应显示 healthy。

### 健康检查

默认端口：

```bash
curl -fsS http://127.0.0.1:7001/health/live && echo
curl -fsS http://127.0.0.1:7001/health/ready && echo
```

自定义端口请替换 `7001`。

### 查看日志（只读）

```bash
cd /opt/flyboard
sudo docker compose logs --tail=100 panel
sudo docker compose logs --tail=100 worker
sudo docker compose logs --tail=100 scheduler
```

持续查看日志按 `Ctrl+C` 退出：

```bash
sudo docker compose logs -f --tail=100 panel worker scheduler
```

### 查看私有管理路径

```bash
sudo cat /opt/flyboard/secrets/admin_path
```

管理地址格式：

```text
https://你的域名/随机管理路径/flyboard
```

---

## 8. 配置 HTTPS 反向代理

Flyboard 快速安装默认绑定 `0.0.0.0:7001`。生产环境推荐在防火墙中限制 7001，并使用 Nginx Proxy Manager、Nginx 或 Caddy 在 443 端口终止 TLS；也可安装时设置 `FLYBOARD_BIND=127.0.0.1`。

### Nginx 示例

先取得管理路径不需要写进 Nginx；整站反代即可。

创建 `/etc/nginx/sites-available/flyboard.conf`：

```nginx
server {
    listen 80;
    server_name panel.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name panel.example.com;

    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    client_max_body_size 10m;

    location / {
        proxy_pass http://127.0.0.1:7001;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;

        # 节点 WebSocket 必需
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;
    }
}
```

启用并验证：

```bash
sudo ln -s /etc/nginx/sites-available/flyboard.conf /etc/nginx/sites-enabled/flyboard.conf
sudo nginx -t
sudo systemctl reload nginx
```

### 启用 Secure Cookie（真正执行，会重建 panel 容器配置）

HTTPS 配置完成后，在 `/opt/flyboard/.env` 写入：

```bash
cd /opt/flyboard
printf 'COOKIE_SECURE=true\n' | sudo tee .env
sudo chmod 600 .env
sudo docker compose up -d panel
```

验证：

```bash
curl -fsS https://panel.example.com/health/ready && echo
```

### Nginx Proxy Manager

- Forward Hostname/IP：`127.0.0.1`（如果 NPM 在宿主网络可访问该地址）或宿主机可达地址
- Forward Port：`7001`
- Scheme：`http`
- 开启 Websockets Support
- SSL 页面申请证书并开启 Force SSL
- 如果 NPM 运行在 Docker 中，容器内的 `127.0.0.1` 指向 NPM 自己，需使用宿主网关地址或把两个项目接入受控共享网络。

---

## 9. 首次登录和功能验收

1. 浏览器打开安装输出的 Admin URL。
2. 输入安装输出的管理员邮箱和一次性密码。
3. 登录后立即确认概览页正常。
4. 依次检查：
   - 主用户
   - 独立客户端
   - 注册节点
   - 上游与上游节点
   - 设备状态
   - Owner Lock 白名单
   - 访问/拦截日志
   - 拉取任务
   - 管理 CIDR
   - 设置
5. 创建一个测试用户，保存只显示一次的订阅 Token。
6. 测试 raw、base64、Clash 和二维码订阅。
7. 创建独立客户端，验证额度、过期时间和设备窗口。
8. 添加测试节点并保存只显示一次的节点 Token。

不要把订阅 Token、节点 Token、管理员路径或 `secrets/` 内容发到公开群组。

---

## 10. 防火墙建议（真正执行，会修改防火墙）

如果使用 UFW：

```bash
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status verbose
```

不要开放 PostgreSQL 5432、Valkey 6379 或 Flyboard 面板端口 7001（除非确需直接访问）到公网。

---

## 11. 备份

### 创建备份（真正执行，会生成备份文件）

```bash
cd /opt/flyboard
sudo ./backup.sh
```

输出类似：

```text
/opt/flyboard/backups/flyboard-20261025T150000Z.tar
```

备份包含：

- PostgreSQL dump
- `compose.yaml`
- 全部 secrets
- SHA-256 manifest

查看备份文件：

```bash
sudo ls -lh /opt/flyboard/backups/
```

必须把备份复制到另一台机器或对象存储；只留在本机不算完整备份。

---

## 12. 恢复

> 恢复会覆盖数据。先确认备份文件正确，并安排维护窗口。

### 只读检查

```bash
sudo tar -tf /opt/flyboard/backups/你的备份.tar | head -50
```

### 真正执行恢复

现有安装包含 secrets 时，必须明确允许替换：

```bash
cd /opt/flyboard
sudo env \
  FLYBOARD_RESTORE_CONFIRM=YES \
  FLYBOARD_RESTORE_SECRETS_CONFIRM=REPLACE \
  ./restore.sh /opt/flyboard/backups/你的备份.tar
```

恢复脚本会先校验路径、文件类型和所有 checksum，并在覆盖前创建一份安全备份。

### 恢复后验证

```bash
cd /opt/flyboard
sudo docker compose ps
curl -fsS http://127.0.0.1:7001/health/ready && echo
sudo docker compose logs --tail=100 panel worker scheduler
```

---

## 13. 升级

正式发布后，把新版本源码覆盖到 `/opt/flyboard`，但不要删除 `secrets/`、`backups/` 和 Docker volumes。

### 升级前只读检查

```bash
cd /opt/flyboard
sudo docker compose ps
df -h /
sudo ls -lh backups/ | tail
```

### 真正执行升级

```bash
cd /opt/flyboard
sudo ./upgrade.sh
```

升级流程会：doctor → 完整备份 → 串行拉取/构建 → 迁移 → 启动 → readiness → doctor。

### 升级后验证

```bash
cd /opt/flyboard
sudo docker compose ps
curl -fsS http://127.0.0.1:7001/health/ready && echo
sudo docker compose logs --tail=100 panel worker scheduler
```

升级失败时，不要删除 `backups/pre-upgrade-*.tar` 和对应镜像清单。

---

## 14. 停止、重启和卸载

### 仅重启服务（真正执行）

```bash
cd /opt/flyboard
sudo docker compose restart panel worker scheduler
```

### 停止并保留数据（真正执行）

```bash
cd /opt/flyboard
sudo ./uninstall.sh --keep-data
```

这会删除项目容器和网络，但保留 Docker volumes、secrets 和 backups。重新运行 `./install.sh` 可恢复启动。

### 永久清除（高风险、不可逆）

先做离机备份。交互执行：

```bash
cd /opt/flyboard
sudo ./uninstall.sh --purge
```

程序会要求输入 `PURGE` 和随机 Compose 项目名进行二次确认。

非交互 purge 虽可使用环境变量，但不建议；误操作会永久删除数据库卷。

---

## 15. 常见问题排查

### 安装卡在镜像构建

```bash
free -h
df -h /
docker system df
```

不要直接执行 `docker system prune -a`，它可能删除其他项目资源。内存不足时优先换到 8 GB 服务器。

### readiness 失败

```bash
cd /opt/flyboard
sudo docker compose ps
sudo docker compose logs --tail=200 postgres valkey panel worker scheduler
```

### 7001 被占用

只读检查：

```bash
sudo ss -lntp | grep ':7001'
```

改用其他端口重新运行：

```bash
cd /opt/flyboard
sudo env FLYBOARD_PORT=18080 ./install.sh
```

之后反向代理端口也改为 18080。

### 忘记管理路径

```bash
sudo cat /opt/flyboard/secrets/admin_path
```

### 忘记管理员密码

安装密码只显示一次，不能从 secrets 中找回。当前 RC1 不建议直接修改数据库；请在验收前妥善保存密码。若全新测试安装尚无数据，可 purge 后重新安装。

### 查看迁移状态和 doctor

```bash
cd /opt/flyboard
sudo docker compose run --rm migrate migrate status
sudo docker compose run --rm migrate doctor
```

---

## 16. 验收结果请回传

安装完成后，请把以下**脱敏信息**发给开发者：

```bash
cd /opt/flyboard
sudo docker compose ps
curl -fsS http://127.0.0.1:7001/health/live
curl -fsS http://127.0.0.1:7001/health/ready
sudo docker compose logs --tail=80 panel worker scheduler
```

回传前删除任何 Token、密码、订阅 URL、公网 IP（如需保密）和管理路径。不要发送整个 `secrets/` 目录。
