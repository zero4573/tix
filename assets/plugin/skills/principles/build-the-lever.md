# Build the lever

Build the tool that does or proves the work (a codemod, script, generator or rerunnable check)
instead of doing it by hand. The bar is non-triviality, not repetition.

- Do the first unit by hand, then build the tool and prove it on that unit. Make it safe to
  rerun.
- Build the smallest script that does the job, never a framework.
- The tool is the evidence a reviewer can rerun: commit it when the work outlives the task.
