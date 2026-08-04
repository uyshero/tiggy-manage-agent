import type {
  CreatedEventSubscription,
  CreateEventSubscriptionRequest,
  EventDelivery,
  EventSubscription,
  EventType,
  UpdateEventSubscriptionRequest,
} from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";

export interface EventDeliveryQuery {
  status?: "pending" | "delivering" | "delivered" | "dead_letter";
  limit?: number;
}

export class EventSubscriptionsService extends ServiceBase {
  eventTypes(signal?: AbortSignal): Promise<EventType[]> {
    return this.transport.requestJSON<{ items: EventType[] }>("GET", "/v2/event-subscriptions/event-types", undefined, signal ? { signal } : {}).then((value) => value.items);
  }

  list(appId?: string, signal?: AbortSignal): Promise<EventSubscription[]> {
    const query = new URLSearchParams();
    if (appId) query.set("app_id", appId);
    const path = "/v2/event-subscriptions" + (query.size ? `?${query}` : "");
    return this.transport.requestJSON<{ items: EventSubscription[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.items);
  }

  create(request: CreateEventSubscriptionRequest, signal?: AbortSignal): Promise<CreatedEventSubscription> {
    return this.transport.requestJSON("POST", "/v2/event-subscriptions", request, signal ? { signal } : {});
  }

  get(subscriptionId: string, signal?: AbortSignal): Promise<EventSubscription> {
    return this.transport.requestJSON("GET", subscriptionPath(subscriptionId), undefined, signal ? { signal } : {});
  }

  update(subscriptionId: string, request: UpdateEventSubscriptionRequest, signal?: AbortSignal): Promise<EventSubscription> {
    return this.transport.requestJSON("PATCH", subscriptionPath(subscriptionId), request, signal ? { signal } : {});
  }

  disable(subscriptionId: string, signal?: AbortSignal): Promise<EventSubscription> {
    return this.transport.requestJSON("DELETE", subscriptionPath(subscriptionId), undefined, signal ? { signal } : {});
  }

  rotateSecret(subscriptionId: string, signal?: AbortSignal): Promise<CreatedEventSubscription> {
    return this.transport.requestJSON("POST", subscriptionPath(subscriptionId) + "/rotate-secret", undefined, signal ? { signal } : {});
  }

  deliveries(subscriptionId: string, query: EventDeliveryQuery = {}, signal?: AbortSignal): Promise<EventDelivery[]> {
    const values = new URLSearchParams();
    if (query.status) values.set("status", query.status);
    if (query.limit && query.limit > 0) values.set("limit", String(query.limit));
    const path = subscriptionPath(subscriptionId) + "/deliveries" + (values.size ? `?${values}` : "");
    return this.transport.requestJSON<{ items: EventDelivery[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.items);
  }

  replay(subscriptionId: string, deliveryId: string, signal?: AbortSignal): Promise<EventDelivery> {
    const path = subscriptionPath(subscriptionId) + "/deliveries/" + encodeURIComponent(deliveryId) + "/replay";
    return this.transport.requestJSON("POST", path, undefined, signal ? { signal } : {});
  }
}

function subscriptionPath(subscriptionId: string): string {
  return resourcePath("/v2/event-subscriptions", subscriptionId);
}
