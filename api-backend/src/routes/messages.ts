import type { FastifyInstance } from "fastify";
import { z } from "zod";

const bodySchema = z.object({
  userId: z.string().min(1),
  data: z.string(),
});

/**
 * POST /messages pushes a message to a single user by publishing to their
 * personal channel (appId:userId) through rtc-backend.
 */
export async function messageRoutes(app: FastifyInstance): Promise<void> {
  app.post("/messages", async (request, reply) => {
    const parsed = bodySchema.safeParse(request.body);
    if (!parsed.success) {
      return reply.code(400).send({ error: "userId and data are required" });
    }
    const { userId, data } = parsed.data;
    const channel = `${app.config.APP_ID}:${userId}`;

    try {
      await app.rtc.publish(channel, data);
    } catch (err) {
      app.log.error({ err, channel }, "failed to publish to rtc-backend");
      return reply.code(502).send({ error: "failed to reach rtc-backend" });
    }

    return reply.code(202).send({ channel });
  });
}
