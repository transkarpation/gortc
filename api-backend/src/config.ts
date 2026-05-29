import { z } from "zod";

const schema = z.object({
  NODE_ENV: z.enum(["development", "production", "test"]).default("development"),
  HOST: z.string().default("0.0.0.0"),
  PORT: z.coerce.number().int().positive().default(3000),
  LOG_LEVEL: z
    .enum(["fatal", "error", "warn", "info", "debug", "trace", "silent"])
    .default("info"),

  // rtc-backend integration.
  RTC_BASE_URL: z.string().url().default("http://localhost:8080"),
  RTC_PUBLISH_SECRET: z.string().min(1),

  // App id, mirroring an app configured in rtc-backend (used for channel names).
  APP_ID: z.string().min(1),

  // Path to the JSON apps file (appId, apiKey, apiSecret per app).
  APPS_FILE: z.string().default("apps.json"),
});

export type Config = z.infer<typeof schema>;

/** loadConfig validates the environment and returns a typed config. */
export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const parsed = schema.safeParse(env);
  if (!parsed.success) {
    const issues = parsed.error.issues
      .map((i) => `${i.path.join(".")}: ${i.message}`)
      .join("; ");
    throw new Error(`invalid configuration: ${issues}`);
  }
  return parsed.data;
}
