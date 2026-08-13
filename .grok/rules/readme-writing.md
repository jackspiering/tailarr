# README writing

When you write or edit `README.md`, follow this rule.

The README is how an operator first meets Tailarr. Write so a tired person
can install the binary, open the TUI, and know what the program does.

This rule applies to prose in `README.md`. It does not change code comments,
error strings, or other Markdown files unless the owner asks.

## Two guides, one voice

Use both:

1. **ASD-STE100** (Issue 9 writing-rule categories). This is the mechanical
   layer: short sentences, one idea, active voice, one meaning per word.
2. **William Zinsser's four principles** from *On Writing Well*. This is the
   quality layer: simple words, no clutter, no fog, a human speaking to a
   human.

ASD-STE100 is a copyrighted standard. Do not copy its dictionary. Do not
claim certified STE compliance. Apply the public rule categories below.

When the two guides pull in different directions, keep the meaning exact and
prefer the operator:

- STE sentence shape wins over a long, flowing sentence.
- Zinsser humanity wins over a military or corporate tone.
- Never drop a condition, limit, or risk to hit a word count.

## Zinsser's four principles

### 1. Simplicity

Strip every sentence to the parts that do work.

- Prefer a short, common word to a long, formal one.
- Prefer a verb to a noun made from a verb (`deploy` not `perform a deployment`).
- Delete marketing adjectives (`seamless`, `robust`, `powerful`,
  `cutting-edge`, `effortless`, `blazing`). Show the fact instead.
- Do not stack hedges (`it is important to note that this may potentially`).

### 2. Brevity

Cut every word that does no work.

- One idea per sentence.
- No throat-clearing (`In order to`, `Please note that`, `It should be
  mentioned that`).
- If a sentence repeats the heading, delete the sentence.
- Shorter is better only while the meaning stays complete.

### 3. Clarity

If the reader is lost, the writer was not careful.

- Name the actor. Use the active voice.
- Use the same name for the same thing every time.
- Be specific. Give the command, the path, or the menu label.
- Keep the strength of every hedge. `may` is not `will`.
- Do not add a cause, a frequency, or a promise the product does not make.

### 4. Humanity

Write as one person to another. The reader is an operator at a terminal,
not a committee.

- Use `you` for the operator.
- Use `Tailarr` for the program. Do not write `the system` or `the solution`.
- Sound like a careful colleague. Do not sound like a brochure.
- Warmth is welcome. Slang, jokes that hide a step, and false cheer are not.
- Write full words (`do not`, `it is`). Contractions are shorter but easier
  to misread.

## ASD-STE100 rules to apply

Apply these structural rules to every prose sentence in `README.md`.

### Words

- One word, one meaning, one part of speech in this file. Pick a verb for an
  action and reuse it.
- Use American English spelling (`behavior`, `canceled`, not `behaviour`,
  `cancelled`) unless a proper name or quote requires otherwise.
- Do not invent slang or jargon as a product name.
- Keep necessary technical names (see [Project names](#project-names)).
  Define a rare term the first time it appears if the meaning is not obvious.
- Prefer a single plain verb to a phrasal verb (`remove` not `take out`,
  `start` not `spin up`, `install` not `set up` when you mean install).

### Verbs and voice

- Prefer infinitive, imperative, simple present, simple past, and simple
  future.
- Use the imperative for steps (`Run the installer.`).
- Use the active voice. Use the passive only when the actor is unknown.
- Do not hide an action in a noun (`Analyze the log` not `Perform an
  analysis of the log`).
- Use an `-ing` form only as a name or part of a name (`logging`,
  `running services`), not as a hanging verb clause when a finite verb is
  clearer.
- Keep a compound verb when the simple form changes the claim (`has
  completed` vs `completed`). Flag that choice if you leave it in.

### Sentences

- Instructions: 20 words or fewer.
- Descriptions: 25 words or fewer.
- One instruction per sentence, unless two actions must occur at the same
  time.
- Do not omit the subject, the verb, or an article to save space.
- Do not use a semicolon. Write two sentences.
- Do not use contractions.
- Use a vertical list for three or more steps, options, or conditions.
- When a sentence starts with a condition, put a comma before the command.
  Example: `If the directory is not writable, install to ~/.local/bin.`

Word-count notes that match STE practice:

- A hyphenated compound counts as one word.
- Text in parentheses counts as one word.
- A colon that starts a list ends the sentence for counting.

### Paragraphs

- One topic per paragraph.
- Six sentences or fewer per paragraph.
- A heading states the topic. The first sentence must not restate the
  heading in different words.

### Safety and risk

Start a warning with a clear command or condition. Then say what can go
wrong.

```text
Do not put a Tailscale auth key on a command flag. The key can leak in
shell history and process lists.
```

Notes give information. Notes do not give commands.

## Project names

Use these names. Do not rotate synonyms.

| Thing | Use this | Do not use |
| --- | --- | --- |
| This program | Tailarr | the tool, the app, the solution, the system |
| The person who runs it | you / operator | user, customer, client, end user |
| The ScaleTail templates | ScaleTail | the upstream repo (except when you mean Git) |
| The service list | catalog | marketplace, library, index |
| Compose project lifecycle | deploy, update, stop, restart, repair, remove | bring up, tear down, spin down |
| Host checks | doctor | diagnostics, healthcheck (in prose) |
| Named Tailscale key | auth key / `TS_AUTHKEY` | token, credential, secret (unless you mean any secret) |
| The interactive program | TUI | dashboard, console, GUI |
| Compose files on the host | Compose | docker-compose as a verb |

Product names, file names, env vars, menu labels, and commands stay exact.

## What this rule does not bind

STE and Zinsser apply to prose. Leave these as they are:

- Fenced commands, shell snippets, and Go build lines
- Paths, URLs, env var names, regex, and mode bits (`600`)
- Badge markup
- Table cells that hold identifiers
- The exact stderr line `Tailarr is interactive; run inside a terminal.`
  (that string is product output, not README voice)

Markdown stays plain ASCII. No curly quotes, em dashes, or decorative
unicode. That matches the repo-wide rule in `AGENTS.md`.

## How to edit the README

1. Read the section. Know what the operator must be able to do after it.
2. Keep every fact, limit, default, and risk that is already true.
3. Rewrite prose to the rules above. Do not rewrite commands that are
   correct.
4. Scan with the checklist.
5. If a sentence must stay long to keep a real limit (a version pin, a path
   rule, a safety condition), keep it and leave the limit in.

Do not "improve" a sentence by adding a claim Tailarr does not make.

## Checklist

Before you call a README change done:

- [ ] Each prose instruction is 20 words or fewer.
- [ ] Each prose description is 25 words or fewer.
- [ ] Each sentence has one idea.
- [ ] Voice is active. Steps use the imperative.
- [ ] No semicolon in prose.
- [ ] No contractions in prose.
- [ ] No synonym rotation for the same thing.
- [ ] No marketing adjectives.
- [ ] No dropped subject, verb, or article.
- [ ] Noun stacks are three words or fewer, or they are a defined technical
      name.
- [ ] `you` speaks to the operator. The tone is human, not corporate.
- [ ] Facts, hedges, and risks match the product.
- [ ] ASCII only.
- [ ] `rumdl check README.md` passes.

## Examples

Weak:

```text
Tailarr is a powerful, seamless solution that allows users to easily
manage their ScaleTail-based Docker Compose deployments from a
best-in-class TUI experience.
```

Strong:

```text
Tailarr deploys and manages ScaleTail Compose services from a TUI.
```

Weak:

```text
In order to get started, you should simply run the installer, which will
automatically detect your OS/arch and take care of putting the binary
in the right place for you.
```

Strong:

```text
Run the installer. It detects your OS and architecture. It then puts
`tailarr` on your `PATH`.
```

Weak:

```text
The file is deleted by the agent after the deployment has been completed
and the user has confirmed the operation.
```

Strong:

```text
After you confirm, Tailarr deletes the file.
```

## Sources

- ASD-STE100 official site: <https://www.asd-ste100.org/>
- ASD Europe, Simplified Technical English:
  <https://www.asd-europe.org/standards-specifications/simplified-technical-english/>
- Public summaries of Issue 9 writing-rule categories (53 rules, nine
  sections). Request the free official copy if you need the dictionary.
- William Zinsser, *On Writing Well*: simplicity, brevity, clarity,
  humanity.
