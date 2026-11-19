-- name: BrandLegalEntities :many
-- BrandLegalEntities is every legal entity, archived ones included, by name:
-- what identity's brand rules pick from and flag a stale rule against
-- (contracts.BrandTargets, brands design B9).
SELECT id, name, (archived_at IS NOT NULL)::boolean AS archived
FROM staff.legal_entities
ORDER BY name COLLATE "C", id;

-- name: BrandLegalEntitiesOfUsers :many
-- BrandLegalEntitiesOfUsers is, for each identity user id in @user_ids, the
-- legal entities under which the person linked to that user (the identity
-- link, provider 'identity') has an employment in force on @today: started and
-- not ended, under a business unit of a legal entity that is not archived. A
-- user with no link or no such employment has no row.
SELECT l.external_id::text AS user_id, bu.legal_entity_id
FROM staff.identity_links l
JOIN staff.employments e ON e.person_id = l.person_id
JOIN staff.business_units bu ON bu.id = e.business_unit_id
JOIN staff.legal_entities le ON le.id = bu.legal_entity_id AND le.archived_at IS NULL
WHERE l.provider = 'identity'
  AND l.external_id = ANY (@user_ids::text[])
  AND e.start_date <= @today::date
  AND (e.end_date IS NULL OR e.end_date >= @today::date)
GROUP BY l.external_id, bu.legal_entity_id
ORDER BY l.external_id, bu.legal_entity_id;
