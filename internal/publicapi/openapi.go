package publicapi

import (
	"net/http"
)

func (s *Server) openAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(openAPISpec))
}

func (s *Server) swaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(swaggerHTML))
}

const swaggerHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>InaTEWS Public API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => SwaggerUIBundle({
      url: '/openapi.json',
      dom_id: '#swagger-ui',
      deepLinking: true,
      persistAuthorization: true,
      displayRequestDuration: true,
      tryItOutEnabled: true
    });
  </script>
</body>
</html>`

const openAPISpec = `{
  "openapi": "3.1.0",
  "info": {
    "title": "InaTEWS Public API",
    "version": "1.1.0",
    "description": "Read-only earthquake API backed by the complete InaTEWS replica in Google Cloud. Data endpoints require an X-API-Key header."
  },
  "tags": [
    {"name": "System", "description": "Service health and readiness"},
    {"name": "Earthquakes", "description": "Earthquake summaries and complete related datasets"}
  ],
  "paths": {
    "/health": {
      "get": {
        "tags": ["System"], "summary": "Liveness check", "operationId": "health",
        "responses": {"200": {"description": "Service is running", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Status"}}}}}
      }
    },
    "/ready": {
      "get": {
        "tags": ["System"], "summary": "Readiness check", "operationId": "ready",
        "responses": {
          "200": {"description": "Database is reachable", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Status"}}}},
          "503": {"$ref": "#/components/responses/ServiceUnavailable"}
        }
      }
    },
    "/v1/earthquakes": {
      "get": {
        "tags": ["Earthquakes"], "summary": "List earthquakes", "operationId": "listEarthquakes",
        "security": [{"ApiKeyAuth": []}],
        "parameters": [
          {"name": "limit", "in": "query", "description": "Number of records (server maximum applies)", "schema": {"type": "integer", "minimum": 1, "maximum": 100, "default": 20}},
          {"name": "cursor", "in": "query", "description": "Opaque next_cursor returned by the preceding response", "schema": {"type": "string"}},
          {"name": "min_mag", "in": "query", "schema": {"type": "number"}},
          {"name": "max_mag", "in": "query", "schema": {"type": "number"}},
          {"name": "status", "in": "query", "schema": {"type": "string"}},
          {"name": "start_time", "in": "query", "description": "Inclusive UTC time", "schema": {"type": "string", "format": "date-time"}},
          {"name": "end_time", "in": "query", "description": "Inclusive UTC time", "schema": {"type": "string", "format": "date-time"}}
        ],
        "responses": {
          "200": {"description": "Earthquake page", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EarthquakeListResponse"}}}},
          "400": {"$ref": "#/components/responses/BadRequest"},
          "401": {"$ref": "#/components/responses/Unauthorized"},
          "403": {"$ref": "#/components/responses/Forbidden"},
          "429": {"$ref": "#/components/responses/RateLimited"}
        }
      }
    },
    "/v1/earthquakes/{eventid}": {
      "get": {
        "tags": ["Earthquakes"], "summary": "Get complete earthquake data", "operationId": "getEarthquake",
        "description": "Returns the earthquake projection, its complete source payload, and all replicated tsunami, moment tensor, felt, damage, narrative, M5, and EQ phase records linked to the event.",
        "security": [{"ApiKeyAuth": []}],
        "parameters": [{"name": "eventid", "in": "path", "required": true, "schema": {"type": "string", "maxLength": 64}}],
        "responses": {
          "200": {"description": "Complete earthquake record", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EarthquakeDetailResponse"}}}},
          "400": {"$ref": "#/components/responses/BadRequest"},
          "401": {"$ref": "#/components/responses/Unauthorized"},
          "403": {"$ref": "#/components/responses/Forbidden"},
          "404": {"$ref": "#/components/responses/NotFound"},
          "429": {"$ref": "#/components/responses/RateLimited"}
        }
      }
    }
  },
  "components": {
    "securitySchemes": {
      "ApiKeyAuth": {"type": "apiKey", "in": "header", "name": "X-API-Key", "description": "InaTEWS API key"}
    },
    "schemas": {
      "Status": {"type": "object", "required": ["status"], "properties": {"status": {"type": "string"}}},
      "Error": {"type": "object", "required": ["error"], "properties": {"error": {"type": "object", "required": ["code", "message"], "properties": {"code": {"type": "string"}, "message": {"type": "string"}}}}},
      "Earthquake": {
        "type": "object",
        "required": ["event_id", "properties", "has_moment_tensor", "has_felt_data", "has_damage_data", "has_narasi", "has_m5_payload", "has_eq_phase", "source_updated_at", "source_sequence"],
        "properties": {
          "event_id": {"type": "string"}, "source_row_id": {"type": "integer", "format": "int64"}, "source": {"type": "string"},
          "magnitude": {"type": "number"}, "depth_km": {"type": "number"}, "latitude": {"type": "number"}, "longitude": {"type": "number"},
          "place": {"type": "string"}, "wib_date": {"type": "string"}, "wib_time": {"type": "string"}, "datetime_utc": {"type": "string", "format": "date-time"},
          "type": {"type": "string"}, "status": {"type": "string"}, "properties": {"type": "object", "additionalProperties": true},
          "has_moment_tensor": {"type": "boolean"}, "has_felt_data": {"type": "boolean"}, "has_damage_data": {"type": "boolean"},
          "has_narasi": {"type": "boolean"}, "has_m5_payload": {"type": "boolean"}, "has_eq_phase": {"type": "boolean"},
          "source_updated_at": {"type": "string", "format": "date-time"}, "source_sequence": {"type": "integer", "format": "uint64", "minimum": 0}
        }
      },
      "RelatedEntity": {
        "type": "object", "required": ["entity_key", "source_updated_at", "source_sequence", "payload"],
        "properties": {
          "entity_key": {"type": "string"}, "source_row_id": {"type": "integer", "format": "int64"},
          "source_updated_at": {"type": "string", "format": "date-time"}, "source_sequence": {"type": "integer", "minimum": 0},
          "payload": {"type": "object", "additionalProperties": true}
        }
      },
      "EarthquakeDetail": {
        "allOf": [
          {"$ref": "#/components/schemas/Earthquake"},
          {"type": "object", "required": ["source_payload", "related"], "properties": {
            "source_payload": {"type": "object", "additionalProperties": true},
            "related": {"type": "object", "description": "Arrays keyed by entity type", "additionalProperties": {"type": "array", "items": {"$ref": "#/components/schemas/RelatedEntity"}}}
          }}
        ]
      },
      "Pagination": {"type": "object", "required": ["has_more", "limit"], "properties": {"next_cursor": {"type": "string"}, "has_more": {"type": "boolean"}, "limit": {"type": "integer"}}},
      "EarthquakeListResponse": {"type": "object", "required": ["data", "pagination"], "properties": {"data": {"type": "array", "items": {"$ref": "#/components/schemas/Earthquake"}}, "pagination": {"$ref": "#/components/schemas/Pagination"}}},
      "EarthquakeDetailResponse": {"type": "object", "required": ["data"], "properties": {"data": {"$ref": "#/components/schemas/EarthquakeDetail"}}}
    },
    "responses": {
      "BadRequest": {"description": "Invalid request", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
      "Unauthorized": {"description": "Missing or invalid API key", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
      "Forbidden": {"description": "API key lacks the required privilege", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
      "NotFound": {"description": "Earthquake was not found", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
      "RateLimited": {"description": "API rate limit exceeded", "headers": {"Retry-After": {"schema": {"type": "integer"}}}, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
      "ServiceUnavailable": {"description": "Service is not ready", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}}
    }
  }
}`
