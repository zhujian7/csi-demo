# csi-demo

A minimal CSI driver built to learn the Container Storage Interface.

- Backend: node-local (a directory, then a loop device)
- Deployment: Controller (Deployment + external-provisioner) and Node (DaemonSet + node-driver-registrar)
- Not implemented: snapshots, cloning, online expansion, topology

Plan and design live in the cubestack repo:
`docs/superpowers/specs/2026-10-08-csi-plugin-learning-design.md`

## Quick start

    make cluster-create
    make deploy
    kubectl get csinodes