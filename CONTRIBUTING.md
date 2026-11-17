<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Contributing

Thank you for your interest in contributing to this project! We value and appreciate any contributions you can make. To maintain a collaborative and respectful environment, please consider the following guidelines when contributing to this project.

## Commit and Pull Request Signature Requirements

When you open a Pull Request (PR), you must ensure that:

1. **Contributor License Agreement (CLA):**
   - You are agreeing to the terms of the CLA by submitting your PR. The CLA document is available [here](https://github.com/InditexTech/foss/blob/main/CLA.md).

2. **Sign Off Each Commit:**
   - Every commit in your PR must include the `Signed-off-by` line in the commit message. This is done by using the `-s` option with `git commit`:

     ```sh
     git commit -s -m "Your commit message"
     ```

   - This line certifies your agreement to the CLA for each contribution. For more details, see the [git commit documentation](https://git-scm.com/docs/git-commit#Documentation/git-commit.txt--s).

3. **GPG-Sign All Commits:**
   - All commits must be signed with a valid and verified GPG signature.
   - The signature must be verified by GitHub and use a verified email address associated with your GitHub account.
   - For instructions on setting up GPG signing, refer to:
     - [GitHub: About commit signature verification](https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification)
     - [Git Book: Signing Your Work](https://git-scm.com/book/en/v2/Git-Tools-Signing-Your-Work)
   - *Tip:* After configuring your client to sign commits, it is advisable to activate signing by default in your local repository:

     ```sh
     git config --local commit.gpgsign true
     ```

   > This ensures all your commits are signed automatically.

## Pull Request Validation Process

When you open a PR:

- An automated comment will confirm that your commit signatures are valid and provide a link to the CLA document you are signing.
- If there are any errors (missing sign-off, invalid GPG signature, or unverified email), the comment will include details about the issue.

Please ensure all requirements are met before submitting your PR to avoid delays in the review process.

## Code of Conduct and Technical Guidelines

All contributions must adhere to the following:

1. **Code of Conduct:**
   - All contributors are expected to follow the project's [Code of Conduct](./CODE_OF_CONDUCT.md). Please be respectful and considerate towards other contributors.

2. **Technical Guidelines:**
   - Contributions must follow the technical guidelines specified in each repository. Please review the repository-specific documentation before starting work.

### General Contribution Guidelines

- Before starting work on a new feature or fix, check existing issues and pull requests to avoid duplications and unnecessary discussions.
- If you wish to work on an existing issue, comment on the issue to inform other contributors that you are working on it. This will help coordinate efforts and prevent conflicts.
- Discuss and gather feedback from the community before making significant changes to the project's structure or architecture.
- Ensure a clean and organized commit history. Divide your changes into logical and descriptive commits. We recommend using the [Conventional Commits Specification](https://www.conventionalcommits.org/en/v1.0.0/).
- Document any new changes or features you add. This will help other contributors and project users understand your work and its purpose.
- Be sure to link the corresponding issue in your pull request to maintain proper tracking of contributions.
- Remember to add license and copyright information following the [REUSE Specification](https://reuse.software/spec/#copyright-and-licensing-information).