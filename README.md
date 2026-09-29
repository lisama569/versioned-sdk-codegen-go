# versioned-sdk-codegen-go

由 schema 定义驱动的版本化 SDK 生成工具链。支持把不同来源、不同写法的
schema 归一化到统一内部模型，对新旧两版 schema 给出可复现的兼容性结论，
并据此生成确定性、可复现的 Go 客户端代码。

## 目录结构

| 路径 | 职责 |
| --- | --- |
| `internal/model` | 归一化后的统一内部模型（`Schema` / `Entity` / `Enum` / `Field`） |
| `internal/schema` | 两种 schema 方言的解析、校验、问题归类 |
| `internal/compat` | 新旧 schema 的差异提取与兼容性判定 |
| `internal/codegen` | 确定性渲染 + 暂存目录原子替换的代码生成 |
| `cmd/sdkgen` | 本地命令行入口（normalize / check / generate） |
| `examples/` | 示例 schema（simple 与 OpenAPI 各一） |

## 支持的 schema 写法

方言由 `--dialect=auto|simple|openapi` 控制，默认 `auto`：
检测到顶层 `openapi` 字段走 OpenAPI 解析，检测到 `entities`/`enums` 走
simple 解析，都无法匹配时返回 `unknown_dialect` 错误，而不是猜测。

### 1. simple（自研简化写法）

```json
{
  "version": "1.0.0",
  "enums": [
    {"name": "Status", "values": ["ACTIVE", "INACTIVE"]}
  ],
  "entities": [
    {"name": "User", "fields": [
      {"name": "Id", "type": "string"},
      {"name": "Age", "type": "integer", "optional": true},
      {"name": "Status", "type": "string", "enum": "Status"}
    ]}
  ]
}
```

- 字段类型仅允许 `string` / `integer` / `number` / `boolean`；
- `optional` 缺省为 `false`（必填）；
- `enum` 引用同文档内的枚举名，引用字段的底层类型必须为 `string`。

### 2. OpenAPI 3.0 子集

- 版本取 `info.version`；
- `components.schemas` 下，`type: "string"` 且带 `enum` 的组件归为枚举，
  `type: "object"` 组件归为实体；
- 属性仅支持四种原始类型，或指向 `#/components/schemas/<枚举名>` 的本地 `$ref`
  （对象/数组引用、跨文件引用均视为非法）；
- 出现在 `required` 数组中的字段为必填，其余为可选；`required` 引用了
  不存在的字段属于畸形输入，会被回报。

## 归一化处理规则

1. **名称必须是合法的 Go 导出标识符**（字母/下划线开头、首字母大写、
   仅含字母数字下划线）。这使生成代码无需再做不可靠的名称映射，
   非法名称直接判 `invalid_name`。
2. **字段缺失、类型非法、命名冲突一律不静默跳过**：解析完整份文档后
   统一回报所有问题，任一 error 级问题都会使整次归一化失败，且不产出
   半成品模型（`Report.Schema == nil`）。
3. **命名冲突按原因区分**：实体/枚举互相重名或重复定义 → `duplicate_name`；
   同一实体内字段重名 → `duplicate_field`；枚举取值重复 →
   `duplicate_enum_value`。
4. **结果确定性**：实体、枚举、字段、枚举取值在内部模型中统一排序，
   OpenAPI 的 `$ref` 解析与组件声明顺序无关（先解析全部枚举再解析实体），
   问题清单按 `(Path, Kind, Message)` 排序。同一份输入反复归一化，
   产物与报错顺序逐字节一致。

问题归类一览：

| Kind | 触发原因 |
| --- | --- |
| `invalid_json` | 输入不是合法 JSON，或根节点不是 JSON 对象 |
| `unknown_dialect` | 无法嗅探方言，或显式指定了不支持的方言 |
| `missing_version` | 缺少 version，或 version 为空白/非字符串 |
| `invalid_shape` | 顶层集合结构错误（如 enums 不是数组） |
| `invalid_name` | 实体/枚举/字段名不是合法 Go 导出标识符 |
| `empty_entry` | 枚举/实体/字段条目不是 JSON 对象 |
| `duplicate_name` | 实体与枚举互相重名，或同名条目重复定义 |
| `missing_field_name` | 字段缺少 name，或 required 引用了不存在的字段 |
| `duplicate_field` | 同一实体内同名字段重复 |
| `invalid_type` | 字段类型缺失/不受支持、optional 非布尔、枚举引用底层不是 string |
| `unknown_enum_ref` | 枚举引用未定义，或 `$ref` 指向不允许的位置 |
| `invalid_enum_value` | 枚举 values 结构非法、为空或含空字符串 |
| `duplicate_enum_value` | 同一枚举内取值重复 |

## 兼容性判定口径

判定方向为「旧 schema → 新 schema：旧版本客户端能否安全消费新版本」。

| 变更 | 结论 | 说明 |
| --- | --- | --- |
| 删除实体 / 删除枚举 | breaking | 旧客户端引用的类型消失 |
| 删除字段 | breaking | 旧客户端仍会发送/读取该字段 |
| 字段类型变更 | breaking | 如 `string` → `integer` |
| 字段可选 → 必填 | breaking | 收紧，旧请求可能缺字段 |
| 字段枚举引用目标变更 | breaking | 取值域整体改变 |
| 删除枚举取值 | breaking | 收紧枚举，持有旧值的数据不再合法 |
| 新增实体 / 新增枚举 | compatible | 纯增量 |
| 新增字段 | compatible | 旧客户端不受影响 |
| 字段必填 → 可选 | compatible | 放宽 |
| 枚举新增取值 | compatible | 放宽 |

结论按 `(Path, Kind)` 排序输出，`Report.IsCompatible()` 仅在破坏性
变更数为 0 时为 true；同一对 schema 反复判定结果一致
（见 `internal/compat` 的确定性测试，连续 20 次结果比对）。

## 代码生成保证

- 全部文件先在内存中渲染并通过 `go/format` 格式化，任一文件失败都不会
  触碰输出目录；
- 写入同级暂存目录后用 rename 整体替换目标目录；旧目录先改名备份，
  新目录就位失败会回滚，**失败不留半截产物**；
- 渲染不读取时间、环境变量或 map 随机顺序，同一 schema 任意次生成
  产物逐字节一致；同一输出目录上的并发生成由互斥锁串行化，
  **并发 N 次的结果与串行完全一致**（见并发测试，N=16）。

## 本地验证

```bash
# 全部单元测试 + 集成测试（-v 可看到每个边界用例打印的输入与判定依据）
go test ./... -v
go test -race -count=1 ./...

# 归一化查看内部模型
go run ./cmd/sdkgen normalize --schema examples/v1.json
go run ./cmd/sdkgen normalize --schema examples/v3_openapi.json

# 兼容性判定（v1→v2 含 3 处破坏性变更，退出码 1；v1→v3 纯增量，退出码 0）
go run ./cmd/sdkgen check --old examples/v1.json --new examples/v2.json
go run ./cmd/sdkgen check --old examples/v1.json --new examples/v3_openapi.json

# 生成 SDK（产物可直接 go build）
go run ./cmd/sdkgen generate --schema examples/v3_openapi.json \
  --out /tmp/sdk --package sdk
(cd /tmp/sdk/.. && go mod init demo 2>/dev/null; go build ./...)
```

`sdkgen` 退出码约定：`0` 成功；`1` 用户输入错误（畸形 schema、
存在破坏性变更）；`2` 工具内部错误或参数错误。
