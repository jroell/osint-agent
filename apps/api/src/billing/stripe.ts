import Stripe from "stripe";
import { config } from "../config";

export const stripe = new Stripe(config.stripe.secretKey, { apiVersion: "2026-03-25.dahlia" });

export async function createCheckoutSession(args: {
  tenantId: string;
  userEmail: string;
  priceId: string;
  successUrl: string;
  cancelUrl: string;
}): Promise<{ url: string }> {
  const session = await stripe.checkout.sessions.create({
    mode: "subscription",
    line_items: [{ price: args.priceId, quantity: 1 }],
    customer_email: args.userEmail,
    client_reference_id: args.tenantId,
    success_url: args.successUrl,
    cancel_url: args.cancelUrl,
  });
  if (!session.url) throw new Error("Stripe did not return a checkout URL");
  return { url: session.url };
}
