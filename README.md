# ci_education

A simple Gin-based API server used for educational purposes.

## Endpoints

- `GET /health` returns `ok`.
- `GET /hello?name=NAME` returns a greeting.
- `GET /pokemon/:name` fetches data from the [PokeAPI](https://pokeapi.co)
  and returns basic information about the given Pokémon.

## Added Features

- Timeout + retry for outbound HTTP calls to PokeAPI.
- Unified JSON error format with request ID header `X-Request-ID`.
- In-memory TTL cache for Pokémon responses (configurable by env var).
- Prometheus metrics at `GET /metrics` (requests, latency, external calls).

## Configuration

- `PORT` (default: `8080`): Server port.
- `POKEAPI_BASE_URL` (default: `https://pokeapi.co/api/v2`): PokeAPI base.
- `HTTP_TIMEOUT_SEC` (default: `5`): HTTP client timeout in seconds.
- `POKEMON_CACHE_TTL_SEC` (default: `300`): Cache TTL in seconds.
