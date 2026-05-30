// Package planning — PlanWriter 接口定义。
package planning

// PlanWriter 将 todo 列表持久化为人类可读的计划文档。
// 定义在 planning 包（使用者侧），供 TodoWriteTool 依赖，避免循环导入。
type PlanWriter interface {
	Write(todos []TodoItem) error
}
