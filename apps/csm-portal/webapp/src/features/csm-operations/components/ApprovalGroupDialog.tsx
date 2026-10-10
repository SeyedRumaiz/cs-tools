// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

import {
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Link,
  List,
  ListItem,
  ListItemText,
  Skeleton,
  Typography,
} from "@wso2/oxygen-ui";
import { useId, type JSX, type ReactNode } from "react";
import QueryErrorState from "@components/QueryErrorState";
import { useGroupDetail } from "@features/csm-operations/api/useGroupDetail";

/** A person listed under "Group Members": a group member, or a customer contact. */
export interface ApprovalGroupPerson {
  id: string;
  name: string;
  email?: string | null;
  /** `"lead"` renders a "Lead" chip. */
  role?: string | null;
}

/**
 * What the dialog opens. A `group` is an internal assignment group, fetched by
 * id (`GET /groups/{id}`) when the dialog opens. The `customer` target is the
 * change request's Customer Group -- the project's registered contacts -- which
 * is not a group row at all, so it is shown from data already on the page
 * (no request).
 */
export type ApprovalGroupTarget =
  | { kind: "group"; id: string; name: string }
  | { kind: "customer"; name: string; contacts: ApprovalGroupPerson[] };

interface ApprovalGroupDialogProps {
  target: ApprovalGroupTarget;
  onClose: () => void;
}

function DetailRow({ label, children }: { label: string; children: ReactNode }): JSX.Element {
  return (
    <>
      <Typography component="dt" variant="body2" color="text.secondary">
        {label}
      </Typography>
      <Typography component="dd" variant="body2" sx={{ m: 0, wordBreak: "break-word" }}>
        {children}
      </Typography>
    </>
  );
}

/** "Group Members (N)" and the people in it: name, email, and a "Lead" chip. */
function MembersList({
  people,
  emptyText,
}: {
  people: ApprovalGroupPerson[];
  emptyText: string;
}): JSX.Element {
  const headingId = useId();
  return (
    <Box component="section" aria-labelledby={headingId} sx={{ display: "flex", flexDirection: "column", gap: 0.5 }}>
      <Typography id={headingId} component="h3" variant="subtitle2">
        Group Members ({people.length})
      </Typography>
      {people.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {emptyText}
        </Typography>
      ) : (
        <List dense disablePadding aria-labelledby={headingId} sx={{ border: 1, borderColor: "divider", borderRadius: 1 }}>
          {people.map((person, index) => (
            <ListItem
              key={`${person.id}-${index}`}
              divider={index < people.length - 1}
              secondaryAction={
                person.role === "lead" ? <Chip size="small" variant="outlined" color="primary" label="Lead" /> : undefined
              }
            >
              <ListItemText
                primary={person.name || person.email || "Unnamed member"}
                secondary={person.email && person.name ? person.email : undefined}
              />
            </ListItem>
          ))}
        </List>
      )}
    </Box>
  );
}

/** The shared chrome: a titled, closable dialog (Esc, backdrop click, Close button). */
function DialogFrame({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}): JSX.Element {
  const titleId = useId();
  return (
    <Dialog open onClose={onClose} maxWidth="sm" fullWidth aria-labelledby={titleId}>
      <DialogTitle id={titleId}>{title}</DialogTitle>
      <DialogContent dividers>{children}</DialogContent>
      <DialogActions>
        <Button onClick={onClose} variant="outlined" color="inherit">
          Close
        </Button>
      </DialogActions>
    </Dialog>
  );
}

/** An internal assignment group, fetched by id once the dialog is open. */
function GroupDialog({
  id,
  name,
  onClose,
}: {
  id: string;
  name: string;
  onClose: () => void;
}): JSX.Element {
  const { data, isLoading, isError, error, refetch } = useGroupDetail(id);

  // The group's own name can differ from the stage's label (renamed since), so
  // the loaded name wins once known; until then the stage's name is the title.
  const title = data?.name || name;

  let body: JSX.Element;
  if (isLoading) {
    body = (
      <Box role="status" aria-label="Loading group members" sx={{ display: "flex", flexDirection: "column", gap: 1 }}>
        <Skeleton variant="rounded" height={20} width="60%" />
        <Skeleton variant="rounded" height={44} />
        <Skeleton variant="rounded" height={44} />
        <Skeleton variant="rounded" height={44} />
      </Box>
    );
  } else if (isError) {
    body = (
      <QueryErrorState
        message="Could not load this group's members."
        error={error}
        onRetry={() => void refetch()}
      />
    );
  } else if (!data) {
    body = (
      <Typography variant="body2" color="text.secondary">
        This group could not be found. It may have been removed.
      </Typography>
    );
  } else {
    const description = data.description?.trim();
    const email = data.email?.trim();
    const manager = data.manager?.name?.trim();
    body = (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {(manager || email || description) && (
          <Box
            component="dl"
            sx={{ display: "grid", gridTemplateColumns: "max-content 1fr", columnGap: 2, rowGap: 0.75, m: 0 }}
          >
            {manager && <DetailRow label="Manager">{manager}</DetailRow>}
            {email && (
              <DetailRow label="Group email">
                <Link href={`mailto:${email}`} underline="hover">
                  {email}
                </Link>
              </DetailRow>
            )}
            {description && <DetailRow label="Description">{description}</DetailRow>}
          </Box>
        )}
        <MembersList
          people={data.members.map((m) => ({ id: m.id, name: m.name, email: m.email, role: m.role }))}
          emptyText="This group has no members."
        />
      </Box>
    );
  }

  return (
    <DialogFrame title={title} onClose={onClose}>
      {body}
    </DialogFrame>
  );
}

/**
 * The page behind an approval stage's Assignment group, like ServiceNow's group
 * form: Manager / Group email / Description when the group has them, and a
 * "Group Members (N)" list. Opens from the Assignment group link on the Approval
 * tab; closes with the Close button, Esc or a click outside, and focus returns
 * to the link that opened it.
 *
 * For the Customer Group (the Customer Approval / Customer Review stages) it
 * shows the project's registered contacts from data already on the page.
 */
export default function ApprovalGroupDialog({ target, onClose }: ApprovalGroupDialogProps): JSX.Element {
  if (target.kind === "group") {
    return <GroupDialog id={target.id} name={target.name} onClose={onClose} />;
  }
  return (
    <DialogFrame title={target.name} onClose={onClose}>
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        <Typography variant="body2" color="text.secondary">
          The registered contacts of this change request&apos;s customer project. They are the customer&apos;s
          approvers at Customer Approval and Customer Review.
        </Typography>
        <MembersList
          people={target.contacts}
          emptyText="This change request's customer project has no registered contacts."
        />
      </Box>
    </DialogFrame>
  );
}
