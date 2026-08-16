# MARS · 宝可梦卡牌库 (Pokémon TCG Library)

单文件 Go 服务器的宝可梦卡牌图鉴/收藏库，浏览器访问，无构建步骤。

![Go](https://img.shields.io/badge/Go-1.25-blue) ![SQLite](https://img.shields.io/badge/SQLite-WAL-green)

## 功能

- 🃏 **卡牌浏览** — 按系列（set）、稀有度、类型、阶段筛选，分页展示
- 🔍 **实时搜索** — 300ms 防抖，搜索卡名 / 编号 / 招式（`?q=` 参数，跨 `card_name`/`card_number`/`attacks` 字段）
- ⭐ **收藏夹** — 收藏/取消收藏卡牌，收藏列表持久化在 SQLite
- 🖼️ **卡图放大** — 点击卡片查看大图与详情（属性、招式、描述）
- 📱 **响应式** — 移动端友好的卡片网格 + 侧边栏
- ⚡ **htmx 局部刷新** — 无整页跳转，丝滑切换

## 技术栈

| 层 | 选择 |
|---|---|
| 后端 | Go 1.25 标准库 + `html/template` |
| 前端 | htmx 2.x + 原生 CSS（无构建步骤） |
| 数据库 | SQLite (`modernc.org/sqlite`, 纯 Go 无 CGO) + WAL 模式 |
| 端口 | `8129` |

## 快速开始

```bash
# 1. 构建
go build -o mars-poke .

# 2. 运行（需 cards.db 就位）
./mars-poke
# → http://localhost:8129
```

systemd（用户级）方式：

```bash
systemctl --user enable --now mars-poke.service
# 状态: systemctl --user status mars-poke.service
# 日志: journalctl --user -u mars-poke.service -f
```

## 路由

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/` | 封面页 |
| GET | `/app` | 主界面（卡牌网格 + 侧边栏） |
| GET | `/cards` | 卡牌网格局部片段（htmx） |
| GET | `/cards/{id}` | 卡牌详情 |
| GET | `/api/sets` | 系列列表 JSON |
| GET | `/api/favorites` | 收藏列表 JSON |
| POST | `/api/favorites` | 添加收藏 |
| DELETE | `/api/favorites/{card_id}` | 取消收藏 |
| GET | `/static/` | 静态资源（图片/CSS/JS） |

## 数据说明

- **数据库** `cards.db`(约 30MB) 由外部脚本生成/填充，**不随仓库分发**（gitignored）
- **卡牌图片** `static/cards/`(约 3.2GB) 是本地资源，同样不随仓库分发
- 首次运行请确保 `cards.db` 与 `static/cards/` 就位，否则列表为空

## 部署

单二进制 + SQLite 文件即可运行，无运行时依赖。适用于 Tailscale 私有网络或个人 NAS。

## 变更日志

见 [CHANGELOG.md](CHANGELOG.md)。
