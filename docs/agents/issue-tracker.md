# Issue tracker: Linear

Issues for this repo live in the `Refermate` Linear workspace. The default team is `Refermate` (`RFM`).

Use the connected Linear operations for issue-tracker work:

- Create or update an issue with `save_issue`. Default new issues to `team: "RFM"`.
- Read an issue with `get_issue` and its discussion with `list_comments`.
- List or search issues with `list_issues`.
- Add a comment with `save_comment`.

Assign an issue to an existing project only when its scope clearly matches that project. The current projects are `Partners Network` and `Gamification`; otherwise leave the project unset.

When changing labels, preserve unrelated labels already attached to the issue. Mark completed work with the `Done` state. Reject work by applying the `wontfix` label and moving the issue to the `Canceled` state.

## When a skill says "publish to the issue tracker"

Create an issue with `save_issue` under team `RFM`. Include the title and full issue body supplied by the skill. Apply a project only under the project-assignment rule above.

## When a skill says "fetch the relevant ticket"

Read the referenced issue with `get_issue`. Read its comments with `list_comments` when discussion or decisions may affect the task.

## Wayfinding operations

Used by `/wayfinder`:

- **Map:** use a parent Linear issue for the effort's Notes, Decisions-so-far, and Fog.
- **Child ticket:** create each ticket with the map issue's ID as `parentId`.
- **Blocking:** represent dependencies with Linear's native `blockedBy` and `blocks` relationships.
- **Frontier:** use `list_issues` to find open, unblocked, and unclaimed child issues.
- **Claim:** update the issue with `assignee: "me"` before beginning work.
- **Resolve:** add the answer or result with `save_comment`, update the map's Decisions-so-far as needed, and move the child issue to `Done`.
