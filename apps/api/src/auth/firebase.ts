import { initializeApp, applicationDefault, cert, getApps } from "firebase-admin/app";
import { getAuth, type DecodedIdToken } from "firebase-admin/auth";
import { config } from "../config";

function initFirebase() {
  if (getApps().length > 0) return;

  // If FIREBASE_SERVICE_ACCOUNT_JSON is set inline (Fly.io secret), use it; otherwise ADC.
  const inlineJson = process.env.FIREBASE_SERVICE_ACCOUNT_JSON;
  if (inlineJson) {
    initializeApp({
      credential: cert(JSON.parse(inlineJson)),
      projectId: config.firebase.projectId,
    });
    return;
  }

  initializeApp({
    credential: applicationDefault(),
    projectId: config.firebase.projectId,
  });
}

/**
 * Verifies a Firebase ID token (JWT) and returns the decoded payload.
 * Throws if invalid, expired, or issuer mismatches our project.
 */
export async function verifyIdToken(idToken: string): Promise<DecodedIdToken> {
  initFirebase();
  return getAuth().verifyIdToken(idToken, true /* checkRevoked */);
}

export type FirebaseUser = DecodedIdToken;
