import type { Config } from "../config.js";

/**
 * RtcClient calls the rtc-backend's internal, secret-guarded endpoints
 * (`POST /publish`, `POST /subscribe`).
 */
export class RtcClient {
  constructor(private readonly config: Config) {}

  /** publish delivers data to every socket subscribed to channel. */
  async publish(channel: string, data: string): Promise<void> {
    await this.post("/publish", { channel, data });
  }

  /** subscribe adds the given connection to the listed channels. */
  async subscribe(connectionId: string, channels: string[]): Promise<void> {
    await this.post("/subscribe", { connectionId, channels });
  }

  private async post(path: string, body: unknown): Promise<void> {
    const res = await fetch(`${this.config.RTC_BASE_URL}${path}`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        authorization: this.config.RTC_PUBLISH_SECRET,
      },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(`rtc ${path} failed: ${res.status} ${text.trim()}`);
    }
  }
}
