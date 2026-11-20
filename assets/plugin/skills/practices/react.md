# React

Read `typescript.md` too. Apply `experience-first` (`tix:principles`) to UI tradeoffs.

| Rule | Enforced by |
|---|---|
| Hook dependency arrays are complete; never silence `react-hooks/exhaustive-deps`: restructure (move the value inside the effect, derive it, or use an event handler) instead | `react-hooks/exhaustive-deps` as an error |
| Hooks are called unconditionally at the top level | `react-hooks/rules-of-hooks` |
| Derive values from props and state during render instead of syncing them into state with an effect | review |
| No `useEffect` for data that comes straight from props or can be computed: compute it (with `useMemo` only when measured as costly); effects are for synchronising with outside systems | review |
| Verify UI changes in the running app, not only in unit tests | review, the ticket's acceptance checks |

More rules are added from retros (`SKILL.md`, "Adding a rule").
