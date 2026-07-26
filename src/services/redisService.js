class RedisService {
  constructor() {
    this.client = null;
  }
  isAlive() {
    return this.client !== null;
  }
}

const redisService = new RedisService();
export default redisService;
