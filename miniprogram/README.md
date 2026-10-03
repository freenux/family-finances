# 家庭财务小程序客户端

薄客户端：只做手机上最值钱的三件事——**手填记账**、**流水列表 + 就地改分类**、**季/年报 KPI**。
所有数据来自服务端 `/api/v1`，契约见 `docs/CLIENT-API-DESIGN.md`（§0 薄客户端第一原则、§9 小程序）。

> **重要：本代码未经真机或模拟器验证。** 写作环境没有微信开发者工具，无法运行小程序。
> 做过的自查只有：所有 JSON 经 `python3 -m json.tool` 校验、所有 JS 经 `node --check` 语法检查、
> `app.json` 注册页面与文件一一对应、页面引用的接口字段逐个对照 `internal/adapter/web/apiv1/` 与 `internal/usecase/` 的 Go 结构体 json tag 核对。
> WXML/WXSS 的渲染效果、`wx.*` API 的实际行为、分包与 tabBar 配置是否被开发者工具接受，均**未验证**，首次导入请预期需要调试。

## 目录结构

```
miniprogram/
  app.js / app.json / app.wxss   入口、页面与分包注册、tabBar、全局样式
  config.js                      API_BASE，唯一的服务地址配置点
  project.config.json            开发者工具项目配置（appid 在此）
  sitemap.json                   不允许被微信索引
  utils/request.js               请求层：注入 Bearer、解信封、按 error.code 分支、showError
  utils/store.js                 Storage 封装：只存 token 与上次选的周期
  utils/meta.js                  /api/v1/meta 的内存缓存（不落 Storage）
  utils/amount.js                元转分 yuanToFen（唯一的客户端换算，见下）
  utils/datetime.js              记账默认时间的格式拼接
  pages/index                    主包：tabBar 首页，三个入口
  pages/mine                     主包：tabBar「我的」，退出登录
  pages/login                    主包：输入 key 换 token
  pkg/pages/tx_new               分包：手填记账
  pkg/pages/tx_list              分包：流水列表、改分类
  pkg/pages/report               分包：季/年报
```

主包只有登录、首页、我的三个页面。tabBar 要求至少两项且必须在主包，所以多了一个很轻的「我的」页。

## 怎么跑起来

1. 微信开发者工具 → 导入项目 → 目录选 `miniprogram/`。
2. appid：改 `project.config.json` 的 `appid`（默认 `touristappid` 游客模式，仅可本地预览）。
3. API 地址：改 `config.js` 的 `API_BASE`，其它地方不要写域名。
4. 开发期可在「详情 → 本地设置」勾选「不校验合法域名、web-view、TLS 版本以及 HTTPS 证书」，用 `http://局域网IP:8787` 调试。

## 部署前提（必须读）

- 小程序 `wx.request` **只能访问 HTTPS**，且域名必须在小程序后台「开发管理 → 开发设置 → 服务器域名 → request 合法域名」白名单里。
- 正式域名**需要 ICP 备案**。
- 自建家庭服务没有备案域名时，可用**微信云托管**跑现成的 Go 容器，从而绕开自备域名配置。
  但云托管容器的文件系统是**临时的**，实例重启或缩容后 SQLite 文件（`family.db`）会丢，
  必须挂持久卷（如云托管的 CFS）或改用托管数据库，否则数据会消失。
- 服务端若启用鉴权，登录页输入的是服务端配置的认证 key；未启用鉴权时服务端返回空 token，客户端不带 Authorization 头也能正常使用。

## 与服务端的分工

不在小程序里做：金额格式化、枚举翻译、分类/专项名查表、周期加减、排序、筛选、合计、规则匹配。
全部使用服务端字段：金额用 `*_text`，枚举用 `*_text`，翻页用 `period.prev/next/has_next`，
科目树用 `/meta.categories`，告警用 `kpi.discretion_warning` 与 `discretion_note`，
流水底部合计用 `totals.*_text`（整个筛选结果集，不是当页）。

唯一例外：手填记账的元→分换算（`utils/amount.js`）。接口只收整数分，用户输入的是元，
所以客户端要换算一次。它是服务端 `usecase.ParseYuanToFen` 的孪生，**改一处必须改另一处**。
该函数不产生任何错误文案，无法解析时返回 0，由服务端返回「金额必须为正数」等提示。

## 本地存储

只存 token 和上次选的周期。**不缓存流水**：`wx.setStorage` 总量上限 10MB、没有索引，
装不下，也无法按条件查询。流水每次都向服务端分页取。

## 刻意没做

- **账单导入**、**分类规则维护**、**AI 财报**：需要大文件/复杂表单/长文阅读，手机体验差，继续留在网页端。
- 批量归入专项、规则批量应用：接口已有，但不属于这三件事。
