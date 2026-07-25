import { rateLimitMiddleware } from '../src/middleware/rateLimiter.js';
import redisService from '../src/services/redisService.js';

describe('Rate Limiter Fail-Closed & Env Security Tests (#2)', () => {
  let req, res, next;

  beforeEach(() => {
    req = { ip: '192.168.1.1', headers: {} };
    res = {
      status: jest.fn().mockReturnThis(),
      json: jest.fn(),
    };
    next = jest.fn();
  });

  it('should deny requests (503 Service Unavailable) when Redis is unreachable', async () => {
    // Mock Redis disconnected state
    jest.spyOn(redisService, 'isAlive').mockReturnValue(false);

    await rateLimitMiddleware(req, res, next);

    expect(res.status).toHaveBeenCalledWith(503);
    expect(res.json).toHaveBeenCalledWith({
      error: 'Service Temporarily Unavailable',
      message: expect.stringContaining('Security protection active'),
    });
    expect(next).not.toHaveBeenCalled();
  });

  it('should deny requests if Redis client throws an exception during evaluation', async () => {
    jest.spyOn(redisService, 'isAlive').mockReturnValue(true);
    redisService.client = {
      incr: jest.fn().mockRejectedValue(new Error('Connection timed out')),
    };

    await rateLimitMiddleware(req, res, next);

    expect(res.status).toHaveBeenCalledWith(503);
    expect(next).not.toHaveBeenCalled();
  });
});