// The hooks module of the example mod: when a session starts, it shows a
// short entry in Claude Code's status line, and changes nothing else.
export function register(on) {
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    $.ui.status('hello from the minimal-pack mod')
    return result
  })
}
