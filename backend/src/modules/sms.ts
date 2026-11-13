import { ApiError } from '../domain/types.js';
import type { OtpSender } from './auth.js';

type Fetch = typeof fetch;

/**
 * Sends the backend's own sign-in code by SMS through MSG91's OTP API
 * (control.msg91.com/api/v5/otp), using a DLT-approved OTP template. The
 * backend still makes, hashes and checks the code; MSG91 only delivers it.
 *
 * Needs MSG91_AUTH_KEY and MSG91_OTP_TEMPLATE_ID (the template's text must
 * contain MSG91's ##OTP## placeholder).
 */
export function msg91OtpSender(opts: { authKey: string; templateId: string; fetch?: Fetch; timeoutMs?: number }): OtpSender {
  const http = opts.fetch ?? fetch;
  return {
    async send(phone, code) {
      const url = new URL('https://control.msg91.com/api/v5/otp');
      url.searchParams.set('template_id', opts.templateId);
      url.searchParams.set('mobile', `91${phone}`);
      url.searchParams.set('otp', code);
      url.searchParams.set('otp_expiry', '5');
      let body: { type?: string; message?: string } = {};
      try {
        const res = await http(url, {
          method: 'POST',
          headers: { authkey: opts.authKey, 'content-type': 'application/json' },
          body: '{}',
          signal: AbortSignal.timeout(opts.timeoutMs ?? 10_000),
        });
        body = (await res.json().catch(() => ({}))) as typeof body;
        if (res.ok && body.type === 'success') return;
      } catch {
        // Falls through to the error below.
      }
      console.error(`[sms] MSG91 did not send to …${phone.slice(-4)}: ${body.message ?? 'no response'}`);
      throw new ApiError(502, 'sms_failed', "We couldn't send the code. Try again in a minute.");
    },
  };
}
