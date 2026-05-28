# Docker DHCP IPAM 插件

一个 Docker IPAM（IP 地址管理）驱动插件，通过 DHCP 从宿主机局域网为容器分配 IP 地址。每个容器通过完整的 DHCP DORA 流程获取一个真实可路由的局域网 IP。

## 工作原理

当容器在此 IPAM 驱动的网络上创建时：

1. 为容器生成唯一 MAC 地址（`02:1a:2b:xx:yy:zz`，本地管理单播地址）
2. 在宿主机物理网卡上执行 DHCP Discover-Offer-Request-Ack (DORA) 流程
3. 租赁到的 IP 地址返回给 Docker 并分配给容器
4. 后台 goroutine 负责在 ~80% T1/租赁时间时自动续约
5. 容器删除时，向 DHCP 服务器发送 DHCPRELEASE

## 前提条件

- Docker CE/EE（在 Docker 29.x 上测试）
- 宿主机连接到有 DHCP 服务器的局域网
- Linux 系统（需要 `CAP_NET_RAW` 使用原始 DHCP 套接字）

## 构建与安装

### 从源码构建

```bash
# 编译插件二进制
make build

# 打包并安装为 Docker managed plugin
make plugin
```

### 手动安装

```bash
# 编译二进制
CGO_ENABLED=0 go build -ldflags="-s -w" -o build/docker-dhcp-ipam-plugin ./cmd/docker-dhcp-ipam-plugin

# 创建 rootfs
mkdir -p plugin-rootfs
docker build -t dhcp-ipam-rootfs .
docker create --name dhcp-ipam-extract dhcp-ipam-rootfs
docker export dhcp-ipam-extract | tar x -C plugin-rootfs/
docker rm dhcp-ipam-extract
cp config.json plugin-rootfs/

# 创建并启用插件
docker plugin create dhcp-ipam plugin-rootfs/
docker plugin enable dhcp-ipam
```

## 使用方法

创建使用 DHCP IPAM 驱动的 macvlan 网络：

```bash
docker network create \
  --driver macvlan \
  --opt parent=eth0 \
  --ipam-driver dhcp-ipam \
  dhcp-net
```

在此网络上运行容器：

```bash
docker run --network dhcp-net --rm alpine ip addr
```

容器将获得局域网 DHCP 服务器分配的 IP 地址。

## 配置

通过环境变量配置插件：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DHCP_IPAM_INTERFACE` | 自动检测 | DHCP 使用的网卡（如 `eth0`、`wlan0`） |
| `DHCP_IPAM_SOCKET_PATH` | `/run/docker/plugins/dhcp_ipam.sock` | Unix 套接字路径 |
| `DHCP_IPAM_LOG_LEVEL` | `info` | 日志级别（`debug`、`info`、`warn`、`error`） |
| `DHCP_IPAM_TIMEOUT` | `10s` | DHCP 请求超时时间 |
| `DHCP_IPAM_RETRIES` | `3` | DHCP 请求重试次数 |

```bash
docker plugin set dhcp-ipam DHCP_IPAM_INTERFACE=eth1
docker plugin set dhcp-ipam DHCP_IPAM_LOG_LEVEL=debug
```

## 架构

```
Docker daemon → Unix 套接字 → IPAM handler → IPAM driver → DHCP 客户端 → 局域网 DHCP 服务器
```

- **cmd/main.go** — HTTP 服务入口，自定义 handler（使用小写 manifest 以兼容 Docker 29.x）
- **pkg/ipam** — 核心驱动，实现 6 个 IPAM API 方法（GetCapabilities、RequestPool、RequestAddress 等）
- **pkg/dhcp** — DHCP 客户端封装（DORA、续约、释放、自动续约循环）
- **pkg/iface** — 自动检测宿主机网卡、子网、网关（通过 `/proc/net/route`）
- **pkg/store** — 线程安全的内存池和租赁存储
- **pkg/config** — 环境变量配置

## 测试

```bash
make test                    # 运行所有测试（含竞态检测）
go test -v ./pkg/iface/...   # 测试网卡检测
go test -v ./pkg/store/...   # 测试存储实现
```

## 已知问题

- **Docker 29.x managed plugin bug**：`docker plugin create` 保存接口类型时会自动添加前缀 `.` 和后缀 `/`，导致 `"ipamdriver"` 能力匹配失败。但 Plugin.Activate 端点返回的 manifest 是正确的。解决方案正在研究中。
- 需要 `CAP_NET_RAW` 权限以使用原始 DHCP 套接字（已在插件配置中包含）。
- 插件使用 `--net=host` 以访问宿主机物理网卡。
- 仅支持 IPv4 DHCP（暂不支持 IPv6/DHCPv6）。

## 许可证

MIT
