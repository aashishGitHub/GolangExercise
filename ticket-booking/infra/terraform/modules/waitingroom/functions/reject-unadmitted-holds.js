// docs/plan.md "waitingroom" module: the EDGE version of the queue — a
// CloudFront Function running at viewer-request, before the request ever
// reaches origin. This is CHEAP PRE-FILTERING ONLY, never the actual
// admission decision: CloudFront Functions have no network access (no
// Redis, no HMAC-key-fetch-from-Secrets-Manager in the general case), so
// this can only reject requests that are STRUCTURALLY missing an
// X-Admission-Token header on the one path the waiting room protects —
// it cannot verify the token's signature. internal/waitingroom's
// RequireAdmission middleware (running in cmd/server-lambda, with the
// real HMAC key) remains the actual security boundary; this function
// only saves a real backend invocation for a request that was NEVER
// going to be admitted anyway (no token at all), the same value
// CloudFront Functions are good at: rejecting garbage before it costs a
// Lambda invocation.
function handler(event) {
  var request = event.request;
  var isHoldsPost =
    request.method === 'POST' && request.uri.indexOf('/holds') !== -1;

  if (isHoldsPost && !request.headers['x-admission-token']) {
    return {
      statusCode: 403,
      statusDescription: 'Forbidden',
      body: {
        encoding: 'text',
        data: '{"code":"not_admitted","message":"join the queue first"}',
      },
    };
  }

  return request;
}
