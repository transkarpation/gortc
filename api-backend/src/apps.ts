import { readFileSync } from "node:fs";

import { z } from "zod";

const appSchema = z.object({
  appId: z.string().min(1),
  apiKey: z.string().min(1),
  apiSecret: z.string().min(1),
});

/** App is a single API consumer, mirroring rtc-backend's apps config. */
export type App = z.infer<typeof appSchema>;

const fileSchema = z.object({
  apps: z.array(appSchema).min(1),
});

/**
 * AppRegistry holds the configured apps indexed by their API key, rejecting
 * duplicate keys (the equivalent of rtc-backend's config validation).
 */
export class AppRegistry {
  private readonly byKey = new Map<string, App>();

  constructor(apps: App[]) {
    for (const app of apps) {
      if (this.byKey.has(app.apiKey)) {
        throw new Error(`duplicate apiKey ${JSON.stringify(app.apiKey)}`);
      }
      this.byKey.set(app.apiKey, app);
    }
  }

  /** byApiKey returns the app for an API key, or undefined if unknown. */
  byApiKey(apiKey: string): App | undefined {
    return this.byKey.get(apiKey);
  }

  /** all returns every configured app. */
  all(): App[] {
    return [...this.byKey.values()];
  }
}

/** loadApps reads and validates a JSON apps file into an AppRegistry. */
export function loadApps(path: string): AppRegistry {
  let raw: string;
  try {
    raw = readFileSync(path, "utf8");
  } catch (err) {
    throw new Error(`read apps config ${path}: ${(err as Error).message}`);
  }

  let json: unknown;
  try {
    json = JSON.parse(raw);
  } catch (err) {
    throw new Error(`parse apps config ${path}: ${(err as Error).message}`);
  }

  const parsed = fileSchema.safeParse(json);
  if (!parsed.success) {
    const issues = parsed.error.issues
      .map((i) => `${i.path.join(".")}: ${i.message}`)
      .join("; ");
    throw new Error(`apps config ${path}: ${issues}`);
  }

  return new AppRegistry(parsed.data.apps);
}
