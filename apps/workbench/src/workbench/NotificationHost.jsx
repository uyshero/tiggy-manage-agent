import React, { useEffect, useState } from "react";
import { createPortal } from "react-dom";

function useNotifications(service) {
  const [notifications, setNotifications] = useState(() => service.getSnapshot());
  useEffect(() => service.subscribe(setNotifications), [service]);
  return notifications;
}

function NotificationItem({ notification, service }) {
  const [paused, setPaused] = useState(false);

  useEffect(() => {
    if (paused || notification.durationMs <= 0) return undefined;
    const timer = window.setTimeout(() => service.dismiss(notification.id), notification.durationMs);
    return () => window.clearTimeout(timer);
  }, [notification.createdAt, notification.durationMs, notification.id, paused, service]);

  const assertive = notification.level === "error" || notification.level === "warning";
  return (
    <article
      className={`workbench-notification ${notification.level}`}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      role={assertive ? "alert" : "status"}
    >
      <span className="workbench-notification-accent" aria-hidden="true" />
      <div className="workbench-notification-copy">
        <strong>{notification.title}</strong>
        {notification.message ? <p>{notification.message}</p> : null}
      </div>
      <button
        className="workbench-notification-close"
        type="button"
        aria-label="关闭通知"
        title="关闭通知"
        onClick={() => service.dismiss(notification.id)}
      >
        ×
      </button>
    </article>
  );
}

export default function NotificationHost({ service }) {
  const notifications = useNotifications(service);
  if (!notifications.length) return null;
  const visible = notifications.slice(-4);

  return createPortal(
    <section className="workbench-notification-region" aria-label="通知">
      {visible.map((notification) => (
        <NotificationItem key={notification.id} notification={notification} service={service} />
      ))}
    </section>,
    document.body
  );
}
