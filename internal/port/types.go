// Package port 定义 aichat 各模块之间的契约。
//
// 这里只有接口与值类型：没有业务实现、不 import 项目内其它包、不 import 第三方库。
// 业务模块之间不互相 import 实现，只 import port —— memory 与 llm 之间因此不会长出循环依赖（docs/02 §4）。
package port

// Scene 是会话场景。
type Scene uint8

const (
	// SceneC2C 是单聊。
	SceneC2C Scene = iota + 1
	// SceneGroup 是群聊。
	SceneGroup
)

// Role 是管理员角色（docs/01 §6.4）。
//
// 注意：这是本项目的 RBAC 权限，不是 QQ 群里的 member_role（两者无关，docs/01 §1.4.2）。
type Role string

const (
	// RoleUser 是普通用户。
	RoleUser Role = "user"
	// RoleGroupAdmin 是群管理员（只能操作授权群）。
	RoleGroupAdmin Role = "group_admin"
	// RoleAdmin 是全局管理员。
	RoleAdmin Role = "admin"
	// RoleOwner 是 owner（唯一，可签发认证码 / reload / shutdown）。
	RoleOwner Role = "owner"
)
