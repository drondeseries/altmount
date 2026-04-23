# Plan: Respect Configured Categories in ARR Auto-Registration

## Objective
Modify the  method in  to check if an instance exists in the current configuration and use its pre-configured category if available, instead of overriding it with hardcoded defaults.

## Key Files & Context
- : Contains the  method and the hardcoded category logic.

## Implementation Steps
1. **Update **:
    - Within the registration flow, before determining the category, check if an instance with the detected  already exists in .
    - If it exists, extract the configured category and use it.
    - If it does not exist, use the existing detection logic (potentially updated to default to a user-preferred category if desired, but primarily ensuring that manual overrides in  are respected).

2. **Verify Registration Flow**:
    - Ensure that subsequent auto-registration attempts do not wipe out manually set categories.

## Verification & Testing
- Manually edit  to set a Whisparr category to .
- Run the registration/auto-setup flow.
- Verify that the category in the config remains  and is not overwritten by .
