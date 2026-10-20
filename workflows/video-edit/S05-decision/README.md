# S05-decision - Content decision (THE ONE TO SETTLE)

**Status:** built twice - qwen/ and video/ engines; A/B pending
**Code:** qwen: select.go + context.go; video: coarse.go, instructions.go, polish.go

Decides what stays. Two engines exist side by side today (see the qwen/ and video/ folders). One A/B on the same video picks the winner; the loser's good ideas get moved into it.

See ../WORKFLOW-DIAGRAMS.md for where this step sits in the line.
