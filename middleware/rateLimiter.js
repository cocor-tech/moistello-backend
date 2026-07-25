import redisService from '../services/redisService.js';

const WINDOW_SIZE_IN_SECONDS = 60;
const MAX_REQUESTS_PER_WINDOW = 100;

/**
 * Enforces rate limiting with a Fail-Closed policy during Redis outages.
 */
export async function rateLimitMiddleware(req, res, next) {
  const clientIp = req.ip || req.headers['x-forwarded-for'] || 'unknown_ip';
  const redisKey = `ratelimit:${clientIp}`;

  try {
    // Verify Redis connection status
    if (!redisService.client || !redisService.isAlive()) {
      console.error(
        `[RateLimiter] [FAIL-CLOSED] Redis client unreachable. Rejecting request from IP: ${clientIp}`
      );
      return res.status(503).json({
        error: 'Service Temporarily Unavailable',
        message: 'Security protection active during system maintenance. Please try again shortly.',
      });
    }

    const currentCount = await redisService.client.incr(redisKey);

    if (currentCount === 1) {
      await redisService.client.expire(redisKey, WINDOW_SIZE_IN_SECONDS);
    }

    if (currentCount > MAX_REQUESTS_PER_WINDOW) {
      return res.status(429).json({
        error: 'Too Many Requests',
        message: 'Rate limit exceeded. Please wait before retrying.',
      });
    }

    next();
  } catch (err) {
    console.error(
      `[RateLimiter] [FAIL-CLOSED] Redis error during evaluation: ${err.message}. Denying access.`
    );
    return res.status(503).json({
      error: 'Service Temporarily Unavailable',
      message: 'Rate limiter failure guard triggered. Access denied.',
    });
  }
}