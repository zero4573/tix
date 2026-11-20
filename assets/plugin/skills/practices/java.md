# Java

Apply `type-system-discipline` and `boundary-discipline` (`tix:principles`) first.

| Rule | Enforced by |
|---|---|
| Model variants as a `sealed interface` of `record`s and switch over it with patterns, without a `default` arm, so a new variant breaks the build where it isn't handled | javac (exhaustive `switch` over a sealed type), review |
| Values are immutable: `record`s, `final` fields, no setters on domain types; return `List.copyOf`/unmodifiable views, never an internal mutable collection | SpotBugs `EI_EXPOSE_REP`/`EI_EXPOSE_REP2`, review |
| Absence is a type: `Optional` for a return value that may be missing (not for fields or parameters); packages are `@NullMarked` (JSpecify) and null-checked | NullAway (Error Prone plugin) |
| External data (HTTP bodies, messages, config, rows) is parsed at the boundary into validated types (Bean Validation on the DTO, then a domain record); no `Map<String, Object>` passed inward | review |
| Every `Closeable` is closed by try-with-resources | Error Prone `MustBeClosedChecker`, SpotBugs `OBL_UNSATISFIED_OBLIGATION` |
| No empty or swallowing `catch`, no `catch (Exception)` to carry on; rethrow with context and the cause (`throw new ConfigException("load " + path, e)`) | PMD `EmptyCatchBlock`, Error Prone `CatchAndPrintStackTrace`, review |
| Logging through SLF4J with placeholders (`log.info("loaded {}", id)`), never concatenation; no `System.out`/`printStackTrace` in shipped code | PMD `SystemPrintln`, Error Prone `CatchAndPrintStackTrace` |
| `equals` and `hashCode` together (a `record` has both) | Error Prone `EqualsHashCode` |
| No mutable static state; dependencies come in through constructors (`final` fields, no field injection) | SpotBugs `MS_MUTABLE_COLLECTION`, review |
| `@SuppressWarnings` names the one check and sits on the smallest element; one that silences a correctness check (`NullAway`, `unchecked` on our own generics) is removed and the code fixed | Error Prone, review |
| Don't mock what you can run: JUnit 5 with real dependencies (Testcontainers for databases and brokers); Mockito only for what can't run locally | review |
| No injection sinks with untrusted input: SQL only with bind parameters (`PreparedStatement`, JPA named parameters), no `ObjectInputStream` or Jackson default typing on untrusted data, XML parsers with external entities off, processes started without a shell; a wrong finding gets `@SuppressFBWarnings(value = "<PATTERN>", justification = "<why>")` on the smallest element | SpotBugs with FindSecBugs (`SQL_INJECTION_*`, `OBJECT_DESERIALIZATION`, `JACKSON_UNSAFE_DESERIALIZATION`, `XXE_*`, `COMMAND_INJECTION`) in the build |
| No dependency with a high or critical advisory: bump it to its fixed version (a transitive one through `dependencyManagement` or a Gradle constraint) | `osv maven` (`tix ws sec-check`) |

Building in a sandbox (Maven and Gradle through the registry proxy, the JDK) is the machine's
`java` skill where it has one, not this file.

More rules are added from retros (`SKILL.md`, "Adding a rule").
