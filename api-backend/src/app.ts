import Fastify, { type FastifyInstance } from "fastify";

import type { Config } from "./config.js";
import { loadApps, type AppRegistry } from "./apps.js";
import { RtcClient } from "./services/rtcClient.js";
import { healthRoutes } from "./routes/health.js";
import { messageRoutes } from "./routes/messages.js";

declare module "fastify" {
  interface FastifyInstance {
    config: Config;
    apps: AppRegistry;
    rtc: RtcClient;
  }
}

/** buildApp wires up a configured Fastify instance (without starting it). */
export async function buildApp(config: Config): Promise<FastifyInstance> {
  const app = Fastify({
    logger: { level: config.LOG_LEVEL },
  });

  const apps = loadApps(config.APPS_FILE);
  app.log.info(`loaded ${apps.all().length} app(s) from ${config.APPS_FILE}`);

  app.decorate("config", config);
  app.decorate("apps", apps);
  app.decorate("rtc", new RtcClient(config));

  await app.register(healthRoutes);
  await app.register(messageRoutes);

  return app;
}
