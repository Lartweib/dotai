// The hooks module of an example mod from dotai's template: a Claude Code
// plugin that a profile names in dotai.yaml. When a session starts, it shows
// a short entry in Claude Code's status line, and changes nothing else.
export function register(on) {
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    $.ui.status('dotai template example mod')
    return result
  })
}
