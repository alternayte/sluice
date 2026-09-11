MODEL (
  name analytics.customer_orders,
  kind FULL,
  grain customer_id
);

-- One row for each customer with the count and the sum of the orders.
SELECT
  c.id AS customer_id,
  c.name AS customer_name,
  COUNT(o.id) AS order_count,
  COALESCE(SUM(o.amount), 0) AS order_amount
FROM raw.customers AS c
LEFT JOIN raw.orders AS o
  ON o.customer_id = c.id
GROUP BY
  c.id,
  c.name
