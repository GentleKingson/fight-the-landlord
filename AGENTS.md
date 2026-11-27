# Skill 调用规则

- 当用户明确以 `$grill-me` 或 `/grill-me` 发起调用时，读取项目根目录下的 `.agents/skills/grill-me/SKILL.md` 和 `.agents/skills/grilling/SKILL.md`，执行其中的工作流程。
- 在 Codex Local 和 Codex Cloud 中均优先使用原生 Skill 调用；若原生调用不可用，则直接读取上述文件并执行对应指令，将 `grill-me` 中对 `grilling` 的调用解析为 `.agents/skills/grilling/SKILL.md`。
