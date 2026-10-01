-- Adds PENDING: an engineer a case is assigned to is now marked PENDING,
-- not BUSY, until they explicitly accept it. BUSY is now reserved for an
-- accepted, in-progress session. PENDING and BUSY both count as "has a
-- current case" everywhere that check already exists, so nothing else
-- here needs to change.
ALTER TYPE chat_routing.engineer_status ADD VALUE 'PENDING';
