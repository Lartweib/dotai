export default {
  id: "rulesync-hooks",
  setup: async (ctx) => {
    const directory = ctx.location?.directory ?? process.cwd();
    const $ = new Bun.$.Shell();
    $.cwd(directory);

    const __rulesyncEvents = new AbortController();
    void (async () => {
      for await (const event of ctx.event.subscribe({ signal: __rulesyncEvents.signal })) {
        if (event.type === "session.created") {
          try {
            await $`echo hi`;
          } catch (error) {
            console.error("[rulesync-hooks] session.created hook failed:", error);
          }
        } else if (event.type === "session.execution.succeeded") {
          try {
            await $`echo bye`;
          } catch (error) {
            console.error("[rulesync-hooks] session.execution.succeeded hook failed:", error);
          }
        }
      }
    })().catch(() => {});

    return async () => {
      __rulesyncEvents.abort();
    };
  },
};
