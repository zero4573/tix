---
type: tool_order
before: { tool: Write, input_match: '"file_path"\s*:\s*"[^"]*/tests/[^"]+\.sh"' }
after: { tool: Edit, input_match: '"file_path"\s*:\s*"[^"]*/clamp\.sh"' }
weight: 3
---
