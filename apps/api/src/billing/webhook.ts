import Stripe from "stripe";
import { sql } from "../db/client";
import { grantCredits } from "./credits";
import { writeEvent } from "../events/stream";
import { stripe } from "./stripe";
import { config } from "../config";
import { logger } from "../telemetry";

const TIER_TO_INCLUDED_CREDITS: Record<string, number> = {
  free: 100 * 100,           // 100 credits = 10_000 millicredits
  hunter: 5000 * 100,        // 5_000 credits = 500_000 millicredits
  operator: 25000 * 100,     // 25_000 credits = 2_500_000 millicredits
};

export async function handleStripeWebhook(rawBody: string, signature: string): Promise<void> {
  let event: Stripe.Event;
  try {
    event = stripe.webhooks.constructEvent(rawBody, signature, config.stripe.webhookSecret);
  } catch (e) {
    logger.error({ err: e }, "stripe webhook signature verification failed");
    throw new Error("bad signature");
  }

  switch (event.type) {
    case "checkout.session.completed": {
      const session = event.data.object as Stripe.Checkout.Session;
      const tenantId = session.client_reference_id;
      if (!tenantId) return;

      // Determine tier from the Price ID
      const subscriptionId = session.subscription as string | null;
      let tier: "hunter" | "operator" = "hunter";
      if (subscriptionId) {
        const sub = await stripe.subscriptions.retrieve(subscriptionId);
        const priceId = sub.items.data[0]?.price?.id;
        if (priceId === config.stripe.priceIdOperator) tier = "operator";
      }

      await sql`
        UPDATE tenants
        SET tier = ${tier},
            stripe_customer_id = ${session.customer as string},
            stripe_subscription_id = ${subscriptionId},
            updated_at = NOW()
        WHERE id = ${tenantId}
      `;

      await grantCredits({
        tenantId,
        millicredits: TIER_TO_INCLUDED_CREDITS[tier]!,
        reason: `refill:${tier}`,
        metadata: { stripe_session: session.id },
      });

      await writeEvent({
        tenantId,
        eventType: "billing.subscription_active",
        payload: { tier, subscription_id: subscriptionId },
      });
      break;
    }

    case "customer.subscription.deleted": {
      const sub = event.data.object as Stripe.Subscription;
      const rows = await sql`
        UPDATE tenants
        SET tier = 'free',
            stripe_subscription_id = NULL,
            updated_at = NOW()
        WHERE stripe_subscription_id = ${sub.id}
        RETURNING id
      `;
      for (const r of rows) {
        await writeEvent({
          tenantId: r.id as string,
          eventType: "billing.subscription_canceled",
          payload: { subscription_id: sub.id },
        });
      }
      break;
    }

    default:
      logger.info({ type: event.type }, "stripe webhook unhandled event");
  }
}
