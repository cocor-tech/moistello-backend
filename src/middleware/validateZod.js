/**
 * Fastify / Express request validation middleware using Zod schemas.
 * Validates request body, query, and params against Zod schemas before handler runs.
 * Returns structured validation errors with field-level messages.
 */

/**
 * Creates a validation middleware function for body, query, and/or params schemas.
 * 
 * @param {Object} schemas - Validation schemas object
 * @param {Object} [schemas.body] - Zod schema for request body
 * @param {Object} [schemas.query] - Zod schema for query params
 * @param {Object} [schemas.params] - Zod schema for URL path params
 * @returns {Function} Pre-handler or middleware function
 */
export function validateRequest({ body, query, params } = {}) {
  return async (req, res, next) => {
    const errors = [];

    if (body && typeof body.safeParse === 'function') {
      const result = body.safeParse(req.body || {});
      if (!result.success) {
        result.error.errors.forEach((err) => {
          errors.push({
            field: `body.${err.path.join('.')}`,
            message: err.message,
          });
        });
      }
    }

    if (query && typeof query.safeParse === 'function') {
      const result = query.safeParse(req.query || {});
      if (!result.success) {
        result.error.errors.forEach((err) => {
          errors.push({
            field: `query.${err.path.join('.')}`,
            message: err.message,
          });
        });
      }
    }

    if (params && typeof params.safeParse === 'function') {
      const result = params.safeParse(req.params || {});
      if (!result.success) {
        result.error.errors.forEach((err) => {
          errors.push({
            field: `params.${err.path.join('.')}`,
            message: err.message,
          });
        });
      }
    }

    if (errors.length > 0) {
      const responsePayload = {
        error: 'Validation Error',
        message: 'Invalid request payload or parameters',
        details: errors,
      };

      // Fastify reply compatibility
      if (res && typeof res.code === 'function') {
        return res.code(400).send(responsePayload);
      }
      if (res && typeof res.status === 'function') {
        return res.status(400).json(responsePayload);
      }
    }

    if (typeof next === 'function') {
      next();
    }
  };
}

/**
 * Fastify plugin for Zod request validation.
 */
export function zodValidationPlugin(fastify, opts, done) {
  fastify.decorate('validateRequest', validateRequest);
  if (typeof done === 'function') {
    done();
  }
}

export default validateRequest;
