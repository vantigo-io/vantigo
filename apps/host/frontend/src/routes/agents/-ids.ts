const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Whether a path parameter is a uuid, so `GET /…/not-an-id` never reaches the API. */
export const isUuid = (value: string) => UUID.test(value);
